package orrery

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Record is one entity as the API lists it.
type Record struct {
	Blueprint string                     `json:"blueprint"`
	Key       string                     `json:"key"`
	Title     string                     `json:"title"`
	Icon      string                     `json:"icon,omitempty"`
	State     string                     `json:"state"`
	Hidden    bool                       `json:"hidden,omitempty"`
	Fields    map[string]json.RawMessage `json:"fields"`
	Relations map[string][]Ref           `json:"relations"`
	Status    Status                     `json:"status"`
}

// Ref is a relation's target.
type Ref struct {
	Blueprint string `json:"blueprint"`
	Key       string `json:"key"`
	Title     string `json:"title"`
}

// Status is the hub's verdict on an entity.
type Status struct {
	Level   string   `json:"level"`
	Reasons []string `json:"reasons"`
}

// String reads a string field; empty when absent or null.
func (r Record) String(name string) string {
	var s string
	_ = json.Unmarshal(r.Fields[name], &s)
	return s
}

// Bool reads a boolean field and whether it was set.
func (r Record) Bool(name string) (bool, bool) {
	var b *bool
	if json.Unmarshal(r.Fields[name], &b) != nil || b == nil {
		return false, false
	}
	return *b, true
}

// Number reads a numeric field and whether it was set.
func (r Record) Number(name string) (float64, bool) {
	var f *float64
	if json.Unmarshal(r.Fields[name], &f) != nil || f == nil {
		return 0, false
	}
	return *f, true
}

// Time reads a datetime field and whether it was set.
func (r Record) Time(name string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, r.String(name))
	return t, err == nil
}

// Strings reads an array of strings.
func (r Record) Strings(name string) []string {
	var out []string
	_ = json.Unmarshal(r.Fields[name], &out)
	return out
}

// Raw reads a field into v, for a shape of the app's own.
func (r Record) Raw(name string, v any) error {
	raw, ok := r.Fields[name]
	if !ok {
		return fmt.Errorf("orrery: %s has no field %s", r.Key, name)
	}
	return json.Unmarshal(raw, v)
}

// Related is the first target of a relation, or false when there is none.
func (r Record) Related(name string) (Ref, bool) {
	refs := r.Relations[name]
	if len(refs) == 0 {
		return Ref{}, false
	}
	return refs[0], true
}

// Filter narrows a list to entities whose field compares to value with op
// (eq, neq, gt, gte, lt, lte, in, contains, prefix, exists, within, older).
type Filter struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value any    `json:"value"`
}

// Query is what to list. State empty lists the hub's default; several
// filters must all match.
type Query struct {
	Blueprint string
	State     string
	Filters   []Filter
}

// PageSize is the most the hub answers per page.
const PageSize = 2000

// Entities lists every entity the query matches, page by page. The token
// must be one that reads: a person's, or an access token.
func (c *Client) Entities(ctx context.Context, q Query) ([]Record, error) {
	var all []Record
	for offset := 0; ; offset += PageSize {
		v := url.Values{"blueprint": {q.Blueprint}, "limit": {strconv.Itoa(PageSize)}, "offset": {strconv.Itoa(offset)}}
		if q.State != "" {
			v.Set("state", q.State)
		}
		if len(q.Filters) > 0 {
			var filter any = q.Filters[0]
			if len(q.Filters) > 1 {
				filter = map[string]any{"and": q.Filters}
			}
			b, err := json.Marshal(filter)
			if err != nil {
				return nil, err
			}
			v.Set("filter", string(b))
		}
		var page struct {
			Entities []Record `json:"entities"`
			Total    int      `json:"total"`
		}
		if err := c.do(ctx, http.MethodGet, "/api/entities?"+v.Encode(), nil, &page); err != nil {
			return nil, fmt.Errorf("%s: %w", q.Blueprint, err)
		}
		all = append(all, page.Entities...)
		if len(page.Entities) < PageSize || len(all) >= page.Total {
			return all, nil
		}
	}
}

// ActionSummary is what a portal shows of an action a person may run.
type ActionSummary struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Group       string `json:"group,omitempty"`
	// Blueprint is set when the action runs on one entity of it.
	Blueprint string `json:"blueprint,omitempty"`
	// Approval is true when a run waits for someone else to approve it.
	Approval bool `json:"approval,omitempty"`
}

// ActionsFor lists the actions the person with this email may run. The
// token's role must hold actions.list.
func (c *Client) ActionsFor(ctx context.Context, email string) ([]ActionSummary, error) {
	var res struct {
		Actions []ActionSummary `json:"actions"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/actions?"+url.Values{"for": {email}}.Encode(), nil, &res); err != nil {
		return nil, err
	}
	return res.Actions, nil
}
