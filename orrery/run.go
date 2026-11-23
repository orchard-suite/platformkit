package orrery

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// RunMode says whether a run is a full snapshot of its scope or a partial
// update. Only a committed snapshot can cause deletions, and only within its
// own source and scope.
type RunMode string

const (
	Snapshot    RunMode = "snapshot"
	Incremental RunMode = "incremental"
)

// RunCounts is what the hub did with a run.
type RunCounts struct {
	Received  int `json:"received"`
	Created   int `json:"created"`
	Updated   int `json:"updated"`
	Unchanged int `json:"unchanged"`
	Rejected  int `json:"rejected"`
	Missing   int `json:"missing"`
	Deleted   int `json:"deleted"`
}

// RunResult is the committed run.
type RunResult struct {
	ID       string       `json:"id"`
	Source   string       `json:"source"`
	Mode     RunMode      `json:"mode"`
	Scope    string       `json:"scope"`
	State    string       `json:"state"`
	Counts   RunCounts    `json:"counts"`
	Errors   []WriteError `json:"errors"`
	MassDrop bool         `json:"massDrop"`
}

// Run is an open run. Add entities, then Commit; on any failure call Fail so
// the hub deletes nothing.
type Run struct {
	client     *Client
	ID         string
	Mode       RunMode
	Scope      string
	batchSize  int
	observedAt string
	buffer     []Entity
	rejected   []WriteError
	sent       int
}

// RunOption tunes a run.
type RunOption func(*runOpts)

type runOpts struct {
	batchSize  int
	observedAt string
	write      string
}

// BatchSize sets how many entities travel per request. Defaults to 200.
func BatchSize(n int) RunOption { return func(o *runOpts) { o.batchSize = n } }

// RunObservedAt stamps every batch with the time the snapshot was taken.
func RunObservedAt(t time.Time) RunOption {
	return func(o *runOpts) { o.observedAt = t.UTC().Format(time.RFC3339Nano) }
}

// EnrichOnly makes the run add fields to existing entities only.
func EnrichOnly() RunOption { return func(o *runOpts) { o.write = "enrich" } }

// OpenRun starts a run over a scope: a cluster, an org, a subscription.
func (c *Client) OpenRun(ctx context.Context, mode RunMode, scope string, opts ...RunOption) (*Run, error) {
	o := runOpts{batchSize: 200, write: "upsert"}
	for _, opt := range opts {
		opt(&o)
	}
	var res struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, http.MethodPost, "/ingest/runs", map[string]string{"mode": string(mode), "scope": scope, "write": o.write}, &res); err != nil {
		return nil, err
	}
	c.log.Info("orrery run opened", "run", res.ID, "mode", mode, "scope", scope)
	return &Run{client: c, ID: res.ID, Mode: mode, Scope: scope, batchSize: o.batchSize, observedAt: o.observedAt}, nil
}

// Add buffers an entity and flushes a full batch.
func (r *Run) Add(ctx context.Context, e Entity) error {
	r.buffer = append(r.buffer, e)
	if len(r.buffer) >= r.batchSize {
		return r.Flush(ctx)
	}
	return nil
}

// Flush sends buffered entities. Updates apply on the hub immediately.
func (r *Run) Flush(ctx context.Context) error {
	if len(r.buffer) == 0 {
		return nil
	}
	body := map[string]any{"entities": r.buffer}
	if r.observedAt != "" {
		body["observedAt"] = r.observedAt
	}
	var res struct {
		Accepted int          `json:"accepted"`
		Rejected []WriteError `json:"rejected"`
	}
	if err := r.client.do(ctx, http.MethodPut, "/ingest/runs/"+url.PathEscape(r.ID)+"/entities", body, &res); err != nil {
		return err
	}
	r.sent += len(r.buffer)
	r.buffer = r.buffer[:0]
	r.rejected = append(r.rejected, res.Rejected...)
	for _, e := range res.Rejected {
		r.client.log.Warn("orrery rejected entity", "run", r.ID, "entity", e.Blueprint+":"+e.Key, "field", e.Field, "reason", e.Reason)
	}
	return nil
}

// Rejected lists every entity the hub refused so far, with the reason.
func (r *Run) Rejected() []WriteError { return r.rejected }

// Commit flushes and commits. A snapshot commit marks everything in the
// scope that the run did not mention as missing.
func (r *Run) Commit(ctx context.Context) (*RunResult, error) {
	if err := r.Flush(ctx); err != nil {
		return nil, err
	}
	var res RunResult
	if err := r.client.do(ctx, http.MethodPost, "/ingest/runs/"+url.PathEscape(r.ID)+"/commit", nil, &res); err != nil {
		return nil, err
	}
	r.client.log.Info("orrery run committed", "run", r.ID, "scope", r.Scope, "sent", r.sent, "created", res.Counts.Created, "updated", res.Counts.Updated, "unchanged", res.Counts.Unchanged, "rejected", res.Counts.Rejected, "missing", res.Counts.Missing, "deleted", res.Counts.Deleted, "massDrop", res.MassDrop)
	return &res, nil
}

// Fail closes the run without committing. Nothing is deleted.
func (r *Run) Fail(ctx context.Context, reason string) error {
	r.client.log.Warn("orrery run failed", "run", r.ID, "reason", reason)
	return r.client.do(ctx, http.MethodPost, "/ingest/runs/"+url.PathEscape(r.ID)+"/fail", map[string]string{"reason": reason}, nil)
}

// Snapshot runs fn inside a snapshot of scope and commits it. If fn returns
// an error the run is failed instead, so a partial read never deletes.
func (c *Client) Snapshot(ctx context.Context, scope string, fn func(add func(Entity) error) error, opts ...RunOption) (*RunResult, error) {
	return c.run(ctx, Snapshot, scope, fn, opts)
}

// Incremental runs fn inside an incremental run of scope and commits it.
// What the run does not mention is left alone, so it suits things that
// happened and only accumulate: deployments, incidents. Give it a scope of
// its own; a snapshot of the same scope would mark its entities missing.
func (c *Client) Incremental(ctx context.Context, scope string, fn func(add func(Entity) error) error, opts ...RunOption) (*RunResult, error) {
	return c.run(ctx, Incremental, scope, fn, opts)
}

func (c *Client) run(ctx context.Context, mode RunMode, scope string, fn func(add func(Entity) error) error, opts []RunOption) (*RunResult, error) {
	run, err := c.OpenRun(ctx, mode, scope, opts...)
	if err != nil {
		return nil, err
	}
	if err := fn(func(e Entity) error { return run.Add(ctx, e) }); err != nil {
		_ = run.Fail(ctx, err.Error())
		return nil, fmt.Errorf("%s run of %s failed: %w", mode, scope, err)
	}
	return run.Commit(ctx)
}
