package annals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// ErrNoClient is what a read on a nil Client answers: there is no Annals
// to read, which is never the same as an Annals that holds nothing.
var ErrNoClient = errors.New("annals: no client")

// ProofKey is what proof is kept by: the effect kind, the blueprint of the
// subject that felt it, and the path the change took. Source, when set,
// counts only what that sender saw.
type ProofKey struct {
	Kind      string
	Blueprint string
	Path      string
	Source    string
}

// Proof is what the watch saw for one key: how many changes it watched,
// each once by the worst of its sightings, and how many of them were
// clean, did harm, or could not be seen. A key never watched counts zero,
// with no LastSeen.
type Proof struct {
	Kind      string     `json:"kind"`
	Blueprint string     `json:"blueprint"`
	Path      string     `json:"path"`
	Source    string     `json:"source,omitempty"`
	Watched   int64      `json:"watched"`
	Clean     int64      `json:"clean"`
	Harm      int64      `json:"harm"`
	Blind     int64      `json:"blind"`
	LastSeen  *time.Time `json:"last_seen,omitempty"`
}

// Proof reads the counts for one key, by key, from the table Annals keeps
// as records arrive; it never reads the records.
func (c *Client) Proof(ctx context.Context, k ProofKey) (*Proof, error) {
	if c == nil {
		return nil, ErrNoClient
	}
	q := url.Values{"kind": {k.Kind}, "blueprint": {k.Blueprint}, "path": {k.Path}}
	if k.Source != "" {
		q.Set("source", k.Source)
	}
	var p Proof
	if err := c.get(ctx, "/v1/proof?"+q.Encode(), 64<<10, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// Query narrows a read of records. A zero field is not a filter. Cause and
// CauseSource together read what one record caused, from every sender.
type Query struct {
	Since       time.Time
	Until       time.Time
	Source      string
	Verb        string
	Cause       string
	CauseSource string
	// Limit is records per page: 50 when zero, at most 500.
	Limit int
	// Cursor is the Next of the page before.
	Cursor string
}

// Page is one page of records, newest first. Next is empty on the last.
type Page struct {
	Events []Event `json:"events"`
	Next   string  `json:"next,omitempty"`
}

// Events reads one page of records.
func (c *Client) Events(ctx context.Context, q Query) (*Page, error) {
	if c == nil {
		return nil, ErrNoClient
	}
	v := url.Values{}
	if !q.Since.IsZero() {
		v.Set("since", q.Since.UTC().Format(time.RFC3339Nano))
	}
	if !q.Until.IsZero() {
		v.Set("until", q.Until.UTC().Format(time.RFC3339Nano))
	}
	for _, f := range []struct{ name, v string }{{"source", q.Source}, {"verb", q.Verb}, {"cause", q.Cause}, {"cause_source", q.CauseSource}, {"cursor", q.Cursor}} {
		if f.v != "" {
			v.Set(f.name, f.v)
		}
	}
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	var p Page
	if err := c.get(ctx, "/v1/events?"+v.Encode(), 64<<20, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// EachEvent reads every record q matches, page after page, newest first,
// and calls f for each. It stops at the first error, from Annals or f.
func (c *Client) EachEvent(ctx context.Context, q Query, f func(Event) error) error {
	for {
		p, err := c.Events(ctx, q)
		if err != nil {
			return err
		}
		for _, ev := range p.Events {
			if err := f(ev); err != nil {
				return err
			}
		}
		if p.Next == "" {
			return nil
		}
		q.Cursor = p.Next
	}
}

func (c *Client) get(ctx context.Context, path string, max int64, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	body := io.LimitReader(res.Body, max)
	if res.StatusCode != http.StatusOK {
		var e struct {
			Error  string `json:"error"`
			Reason string `json:"reason"`
		}
		_ = json.NewDecoder(body).Decode(&e)
		return fmt.Errorf("annals answered %d %s %s", res.StatusCode, e.Reason, e.Error)
	}
	if err := json.NewDecoder(body).Decode(out); err != nil {
		return fmt.Errorf("annals' answer: %w", err)
	}
	return nil
}
