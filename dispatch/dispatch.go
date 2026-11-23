// Package dispatch is the incident desk's client for a product: it sends
// the product's events, declares their kinds, records its changes, checks
// in for its heartbeat, and reads open incidents and who is on call.
//
// A nil Client sends nothing and reads nothing, so a product without a
// desk calls it like any other.
package dispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/orchard-suite/platformkit/v2/telemetry"
)

// Weight is how hard an event pushes on the people it reaches.
type Weight string

const (
	// Notice lands in a person's list and tells them nothing more.
	Notice Weight = "notice"
	// Notify reaches a person by their notify rule, after quiet hours.
	Notify Weight = "notify"
	// Page starts each escalation chain the event reaches.
	Page Weight = "page"
)

// The severities of an event and of an incident.
const (
	Critical = "critical"
	Warning  = "warning"
	Info     = "info"
)

// Ref names one catalog entity.
type Ref struct {
	Blueprint string `json:"blueprint"`
	Key       string `json:"key"`
	Title     string `json:"title,omitempty"`
}

// Fact is one named value an event shows.
type Fact struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Link is one place an event points to.
type Link struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

// Target is who to tell besides the channels: exactly one of a person by
// address, a group by slug, or an escalation chain by slug.
type Target struct {
	Person string `json:"person,omitempty"`
	Group  string `json:"group,omitempty"`
	Chain  string `json:"chain,omitempty"`
}

// Event is what happened, as the desk takes it. Kind is dotted words, such
// as request.answered. Key is the product's own id for the event, so a
// send made twice is stored once.
type Event struct {
	Kind     string            `json:"kind"`
	Key      string            `json:"key,omitempty"`
	Title    string            `json:"title,omitempty"`
	Weight   Weight            `json:"weight,omitempty"`
	Text     string            `json:"text,omitempty"`
	Severity string            `json:"severity,omitempty"`
	Entity   *Ref              `json:"entity,omitempty"`
	Facts    []Fact            `json:"facts,omitempty"`
	Links    []Link            `json:"links,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
	To       []Target          `json:"to,omitempty"`
	At       *time.Time        `json:"at,omitempty"`
}

// Kind is one kind of event a product sends, described for the people who
// choose how they are told about it.
type Kind struct {
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Weight      Weight `json:"weight"`
}

// Incident is an incident as the desk lists it.
type Incident struct {
	ID       int64     `json:"id"`
	Title    string    `json:"title"`
	Severity string    `json:"severity"`
	State    string    `json:"state"`
	Service  string    `json:"service"`
	OpenedAt time.Time `json:"opened_at"`
	// URL is the incident's page, known for an incident this client opened.
	URL string `json:"-"`
}

// NewIncident opens an incident on a service.
type NewIncident struct {
	Title       string `json:"title"`
	Severity    string `json:"severity,omitempty"`
	Service     string `json:"service,omitempty"`
	Description string `json:"description,omitempty"`
}

// Change is a deploy or another change to a service.
type Change struct {
	Service string     `json:"service"`
	Title   string     `json:"title"`
	Kind    string     `json:"kind,omitempty"`
	URL     string     `json:"url,omitempty"`
	By      string     `json:"by,omitempty"`
	At      *time.Time `json:"at,omitempty"`
}

// OnCall is one schedule and who it has on call now. Person is empty when
// nobody is.
type OnCall struct {
	Slug   string    `json:"slug"`
	Name   string    `json:"name"`
	Person string    `json:"person"`
	Until  time.Time `json:"until"`
}

// Error is an answer of 400 or above.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("dispatch answered %d: %s", e.Status, e.Message) }

// Client calls one desk's API port with one product's token.
type Client struct {
	url   string
	token string
	http  *http.Client
}

// New returns a client for the desk's API at url, or nil when url or token
// is empty. The desk names the product by the token.
func New(url, token string) *Client {
	if url == "" || token == "" {
		return nil
	}
	return &Client{url: strings.TrimRight(url, "/"), token: token, http: telemetry.Client(15 * time.Second)}
}

// Send sends one event.
func (c *Client) Send(ctx context.Context, ev Event) error {
	if c == nil {
		return nil
	}
	return c.call(ctx, http.MethodPost, "/v1/events", ev, nil, nil)
}

// DeclareKinds tells the desk every kind of event this product sends. It
// replaces what the product declared before, so a product declares its
// whole set each time it starts.
func (c *Client) DeclareKinds(ctx context.Context, kinds []Kind) error {
	if c == nil {
		return nil
	}
	return c.call(ctx, http.MethodPut, "/v1/kinds", map[string][]Kind{"kinds": kinds}, nil, nil)
}

// OpenIncidents lists the incidents on a service that are open or
// acknowledged, newest first as the desk orders them.
func (c *Client) OpenIncidents(ctx context.Context, service string) ([]Incident, error) {
	if c == nil {
		return nil, nil
	}
	q := url.Values{"state": {"open,acknowledged"}, "service": {service}}
	var out []Incident
	if err := c.call(ctx, http.MethodGet, "/v1/incidents?"+q.Encode(), nil, &out, nil); err != nil {
		return nil, err
	}
	return out, nil
}

// OpenIncident opens an incident. A nil client opens none and answers nil.
func (c *Client) OpenIncident(ctx context.Context, in NewIncident) (*Incident, error) {
	if c == nil {
		return nil, nil
	}
	var out Incident
	var header http.Header
	if err := c.call(ctx, http.MethodPost, "/v1/incidents", in, &out, &header); err != nil {
		return nil, err
	}
	out.URL = header.Get("Location")
	return &out, nil
}

// RecordChange records a deploy or another change to a service.
func (c *Client) RecordChange(ctx context.Context, ch Change) error {
	if c == nil {
		return nil
	}
	return c.call(ctx, http.MethodPost, "/v1/changes", ch, nil, nil)
}

// Heartbeat checks in under a name. The first check-in makes the heartbeat,
// expected this often.
func (c *Client) Heartbeat(ctx context.Context, name string, every time.Duration) error {
	if c == nil {
		return nil
	}
	return c.call(ctx, http.MethodPost, "/v1/heartbeats/"+url.PathEscape(name)+"?"+url.Values{"interval": {every.String()}}.Encode(), nil, nil, nil)
}

// OnCall lists every schedule with who is on call now.
func (c *Client) OnCall(ctx context.Context) ([]OnCall, error) {
	if c == nil {
		return nil, nil
	}
	var out []OnCall
	if err := c.call(ctx, http.MethodGet, "/v1/schedules", nil, &out, nil); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *Client) call(ctx context.Context, method, path string, in, out any, header *http.Header) error {
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return err
	}
	if res.StatusCode >= 400 {
		var parsed struct {
			Error string `json:"error"`
		}
		msg := strings.TrimSpace(string(raw))
		if json.Unmarshal(raw, &parsed) == nil && parsed.Error != "" {
			msg = parsed.Error
		}
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return &Error{Status: res.StatusCode, Message: msg}
	}
	if header != nil {
		*header = res.Header
	}
	if out != nil && len(raw) > 0 {
		return json.Unmarshal(raw, out)
	}
	return nil
}
