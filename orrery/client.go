// Package orrery is the client every app, pusher, runner, and shell job uses
// to talk to an Orrery hub: reads, auth, runs, scopes, batching, retries,
// observedAt, heartbeat, and validation-error reporting. A pusher built on it
// only fetches and shapes.
package orrery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/orchard-suite/platformkit/v3/telemetry"
)

// Entity is one thing to write. Icon names one icon of the hub's icon set for
// this entity alone; leave it empty and the entity shows its blueprint's
// icon, which a person may then change. Relations hold target ids ("blueprint:key")
// or bare keys of the relation's target blueprint; a nil value clears the
// relation at the source's layer.
type Entity struct {
	Blueprint string         `json:"blueprint"`
	Key       string         `json:"key"`
	Title     string         `json:"title,omitempty"`
	Icon      string         `json:"icon,omitempty"`
	Fields    map[string]any `json:"fields,omitempty"`
	Relations map[string]any `json:"relations,omitempty"`
}

// ID returns the entity identifier the hub uses.
func (e Entity) ID() string { return e.Blueprint + ":" + e.Key }

// WriteError names what the hub refused and why.
type WriteError struct {
	Blueprint string `json:"blueprint,omitempty"`
	Key       string `json:"key,omitempty"`
	Field     string `json:"field,omitempty"`
	Reason    string `json:"reason"`
}

func (e WriteError) String() string {
	s := e.Blueprint + ":" + e.Key
	if e.Field != "" {
		s += "." + e.Field
	}
	return s + ": " + e.Reason
}

// APIError is a non-2xx answer from the hub.
type APIError struct {
	Status  int
	Message string
	Errors  []WriteError
	// Index is the place in a batch of the item the hub refused, when its
	// answer names one: the refusal is for that item's content, and sending
	// it again will not help.
	Index *int
}

func (e *APIError) Error() string {
	if len(e.Errors) > 0 {
		return fmt.Sprintf("orrery: %d %s (%d rejected, first: %s)", e.Status, e.Message, len(e.Errors), e.Errors[0])
	}
	return fmt.Sprintf("orrery: %d %s", e.Status, e.Message)
}

// Client talks to one hub as one source.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
	log     *slog.Logger
	retries int
}

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient replaces the HTTP client.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

// WithLogger sets the logger. Defaults to slog.Default().
func WithLogger(l *slog.Logger) Option { return func(c *Client) { c.log = l } }

// WithRetries sets how many times a failed request is retried on network
// errors and 5xx answers. Defaults to 4.
func WithRetries(n int) Option { return func(c *Client) { c.retries = n } }

// New returns a client for the hub's machine surface, e.g.
// https://orrery-ingest.example.org, authenticating with a source or
// access token.
func New(baseURL, token string, opts ...Option) *Client {
	c := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    telemetry.Client(60 * time.Second),
		log:     slog.Default(),
		retries: 4,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// FromEnv builds a client from ORRERY_URL and ORRERY_TOKEN.
func FromEnv(opts ...Option) (*Client, error) {
	u := os.Getenv("ORRERY_URL")
	t := os.Getenv("ORRERY_TOKEN")
	if u == "" || t == "" {
		return nil, errors.New("ORRERY_URL and ORRERY_TOKEN must be set")
	}
	return New(u, t, opts...), nil
}

// Heartbeat marks the source alive without opening a run.
func (c *Client) Heartbeat(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/v1/ingest/heartbeat", nil, nil)
}

// WriteOption tunes a single-entity write.
type WriteOption func(*writeBody)

type writeBody struct {
	Title      string         `json:"title,omitempty"`
	Icon       string         `json:"icon,omitempty"`
	Fields     map[string]any `json:"fields,omitempty"`
	Relations  map[string]any `json:"relations,omitempty"`
	ObservedAt string         `json:"observedAt,omitempty"`
	Mode       string         `json:"mode,omitempty"`
	Scope      string         `json:"scope,omitempty"`
}

// ObservedAt tells the hub when the value was true; older writes are ignored.
func ObservedAt(t time.Time) WriteOption {
	return func(b *writeBody) { b.ObservedAt = t.UTC().Format(time.RFC3339Nano) }
}

// Enrich only adds fields to entities that already exist and never keeps
// them alive.
func Enrich() WriteOption { return func(b *writeBody) { b.Mode = "enrich" } }

// InScope records the scope on entities this write creates.
func InScope(scope string) WriteOption { return func(b *writeBody) { b.Scope = scope } }

// Put creates or replaces one entity.
func (c *Client) Put(ctx context.Context, e Entity, opts ...WriteOption) error {
	return c.write(ctx, http.MethodPut, e, opts)
}

// Patch merges the given fields into one entity.
func (c *Client) Patch(ctx context.Context, e Entity, opts ...WriteOption) error {
	return c.write(ctx, http.MethodPatch, e, opts)
}

func (c *Client) write(ctx context.Context, method string, e Entity, opts []WriteOption) error {
	body := writeBody{Title: e.Title, Icon: e.Icon, Fields: e.Fields, Relations: e.Relations}
	for _, o := range opts {
		o(&body)
	}
	return c.do(ctx, method, "/v1/ingest/entities/"+url.PathEscape(e.Blueprint)+"/"+url.PathEscape(e.Key), body, nil)
}

// Delete removes an entity now. The source needs a delete grant for the
// blueprint; the hub blocks re-creation from snapshots for a short window.
func (c *Client) Delete(ctx context.Context, blueprint, key string) error {
	return c.do(ctx, http.MethodDelete, "/v1/ingest/entities/"+url.PathEscape(blueprint)+"/"+url.PathEscape(key), nil, nil)
}

// Call sends one request to the hub's machine surface and decodes the JSON
// answer into out. It is what the runner protocol and anything not covered
// by the typed helpers use.
func (c *Client) Call(ctx context.Context, method, path string, in, out any) error {
	return c.do(ctx, method, path, in, out)
}

// RefreshGitSource asks the hub to pull a git source now. The token must
// belong to that source.
func (c *Client) RefreshGitSource(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/v1/ingest/sources/"+url.PathEscape(id)+"/refresh", nil, nil)
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	return c.exchange(ctx, request{method: method, path: path, accept: "application/json"}, in, out)
}

// doOnce is do for a call that must land at most once and may take long.
func (c *Client) doOnce(ctx context.Context, method, path string, in, out any) error {
	return c.exchange(ctx, request{method: method, path: path, accept: "application/json", once: true, wait: true}, in, out)
}

func (c *Client) exchange(ctx context.Context, req request, in, out any) error {
	if in != nil {
		var err error
		if req.body, err = json.Marshal(in); err != nil {
			return err
		}
		req.contentType = "application/json"
	}
	res, err := c.send(ctx, req)
	if err != nil {
		return err
	}
	if out != nil && len(res.body) > 0 {
		return json.Unmarshal(res.body, out)
	}
	return nil
}

// request is one call to the hub, whatever its body.
type request struct {
	method      string
	path        string
	accept      string
	contentType string
	body        []byte
	header      http.Header
	// once sends the call a single time: a call that does something each
	// time it lands is never sent twice.
	once bool
	// wait lets the answer take as long as the caller's context allows.
	wait bool
}

// response is a hub answer below 400.
type response struct {
	status int
	header http.Header
	body   []byte
}

// send makes one call to the hub, retrying network errors and 5xx answers,
// and turns a 4xx answer into an *APIError.
func (c *Client) send(ctx context.Context, r request) (*response, error) {
	var last error
	retries, client := c.retries, c.http
	if r.once {
		retries = 0
	}
	if r.wait {
		unhurried := *c.http
		unhurried.Timeout = 0
		client = &unhurried
	}
	for attempt := 0; attempt <= retries; attempt++ {
		if attempt > 0 {
			wait := time.Duration(1<<uint(attempt-1))*time.Second + time.Duration(rand.IntN(500))*time.Millisecond
			c.log.Warn("orrery request retry", "method", r.method, "path", r.path, "attempt", attempt, "wait", wait, "error", last)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(wait):
			}
		}
		req, err := http.NewRequestWithContext(ctx, r.method, c.baseURL+r.path, bytes.NewReader(r.body))
		if err != nil {
			return nil, err
		}
		for k, v := range r.header {
			req.Header[k] = v
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Accept", r.accept)
		if r.contentType != "" {
			req.Header.Set("Content-Type", r.contentType)
		}
		res, err := client.Do(req)
		if err != nil {
			last = err
			continue
		}
		body, err := io.ReadAll(io.LimitReader(res.Body, 256<<20))
		res.Body.Close()
		if err != nil {
			last = err
			continue
		}
		if res.StatusCode >= 500 {
			last = &APIError{Status: res.StatusCode, Message: strings.TrimSpace(string(body))}
			continue
		}
		if res.StatusCode >= 400 {
			apiErr := &APIError{Status: res.StatusCode}
			var parsed struct {
				Error  string       `json:"error"`
				Errors []WriteError `json:"errors"`
				Index  *int         `json:"index"`
			}
			if json.Unmarshal(body, &parsed) == nil && parsed.Error != "" {
				apiErr.Message = parsed.Error
				apiErr.Errors = parsed.Errors
				apiErr.Index = parsed.Index
			} else {
				apiErr.Message = strings.TrimSpace(string(body))
			}
			return nil, apiErr
		}
		return &response{status: res.StatusCode, header: res.Header, body: body}, nil
	}
	return nil, fmt.Errorf("orrery: giving up after %d attempts: %w", retries+1, last)
}

// ActionReport is what a backend tells the hub about an action run. Log
// lines are appended; a terminal State (succeeded or failed) closes the run.
type ActionReport struct {
	State   string   `json:"state,omitempty"`
	Label   string   `json:"label,omitempty"`
	Link    string   `json:"link,omitempty"`
	Summary string   `json:"summary,omitempty"`
	Log     []string `json:"log,omitempty"`
	// Step names the step now under way. Log lines sent with it sit under it.
	Step string `json:"step,omitempty"`
	// Outputs are values to show on the run, by name. A nil value removes one.
	Outputs map[string]any `json:"outputs,omitempty"`
}

// ReportActionRun reports progress on an action run from a backend such as a
// workflow or a webhook target. The token is an access token whose role holds
// actions.report.
func (c *Client) ReportActionRun(ctx context.Context, runID string, report ActionReport) error {
	return c.do(ctx, http.MethodPost, "/v1/action-runs/"+url.PathEscape(runID)+"/report", report, nil)
}
