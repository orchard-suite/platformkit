package orrery

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// EntityRef names one entity by its blueprint and key.
type EntityRef struct {
	Blueprint string `json:"blueprint"`
	Key       string `json:"key"`
}

// ID is the entity's id as the hub writes it: blueprint:key.
func (r EntityRef) ID() string { return r.Blueprint + ":" + r.Key }

// ParseRef reads an entity id such as repository:shop-api. The key may hold
// colons; the blueprint may not.
func ParseRef(id string) (EntityRef, error) {
	blueprint, key, ok := strings.Cut(id, ":")
	if !ok || blueprint == "" || key == "" {
		return EntityRef{}, fmt.Errorf("orrery: %q is not an entity id (blueprint:key)", id)
	}
	return EntityRef{Blueprint: blueprint, Key: key}, nil
}

// The states of an action run.
const (
	RunAwaiting  = "awaiting"
	RunQueued    = "queued"
	RunRunning   = "running"
	RunSucceeded = "succeeded"
	RunFailed    = "failed"
	RunDeclined  = "declined"
	RunCancelled = "cancelled"
	RunReturned  = "returned"
)

// The tiers of a tool.
const (
	TierRead = "read"
	TierData = "data"
	TierAct  = "act"
)

// ActionRun is one time an action was asked for, and what came of it.
type ActionRun struct {
	ID     string         `json:"id"`
	Action string         `json:"action"`
	Entity *EntityRef     `json:"entity"`
	Inputs map[string]any `json:"inputs"`
	// By is who asked.
	By      string `json:"by"`
	State   string `json:"state"`
	Label   string `json:"label"`
	Link    string `json:"link"`
	Summary string `json:"summary"`
	// DecidedBy is who approved it, declined it, or sent it back.
	DecidedBy string `json:"decidedBy"`
	DecidedAt string `json:"decidedAt"`
	// Tiers are the tool tiers the run asked for beyond its action's.
	Tiers []string `json:"tiers"`
	// Revises is the run this one asks again for.
	Revises string `json:"revises"`
	// Note is what whoever sent it back wrote.
	Note      string `json:"note"`
	CreatedAt string `json:"createdAt"`
}

// Finished reports whether the run is over, however it ended.
func (r ActionRun) Finished() bool {
	switch r.State {
	case RunSucceeded, RunFailed, RunDeclined, RunCancelled, RunReturned:
		return true
	}
	return false
}

// RunRequest asks for one run of an action.
type RunRequest struct {
	// Entity is what the run is on. Leave it nil for a global action.
	Entity *EntityRef     `json:"entity,omitempty"`
	Inputs map[string]any `json:"inputs"`
	// Tiers asks for tool tiers beyond the action's own. Such a run always
	// waits for someone who holds each of them.
	Tiers []string `json:"tiers,omitempty"`
	// Revises names the run this one asks again for, after it was sent back.
	Revises string `json:"revises,omitempty"`
	// For is the sign-in address of the person the run is asked for. The
	// token's role must hold actions.ask.
	For string `json:"for,omitempty"`
}

// RunAction asks for a run. The hub judges it by the rules of whoever it is
// for; a refusal is an *APIError.
func (c *Client) RunAction(ctx context.Context, id string, req RunRequest) (*ActionRun, error) {
	if req.Inputs == nil {
		req.Inputs = map[string]any{}
	}
	var run ActionRun
	if err := c.do(ctx, http.MethodPost, "/v1/actions/"+url.PathEscape(id)+"/run", req, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

// ActionRun reads one run.
func (c *Client) ActionRun(ctx context.Context, id string) (*ActionRun, error) {
	var run ActionRun
	if err := c.do(ctx, http.MethodGet, "/v1/action-runs/"+url.PathEscape(id), nil, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

// WithdrawRun takes back a run that has not started.
func (c *Client) WithdrawRun(ctx context.Context, id, reason string) error {
	return c.do(ctx, http.MethodPost, "/v1/action-runs/"+url.PathEscape(id)+"/withdraw", map[string]string{"reason": reason}, nil)
}

// Target is one entity a person may run an action on.
type Target struct {
	EntityRef
	Title string
	// Waits is true when a run there waits for approval.
	Waits bool
}

// TargetsFor lists where the person with this sign-in address may run the
// action, judged by the action's rules alone. Someone the hub has not met
// holds nothing. The token's role must hold actions.list.
func (c *Client) TargetsFor(ctx context.Context, action, email string) ([]Target, error) {
	var res struct {
		Entities []struct {
			Blueprint string `json:"blueprint"`
			Key       string `json:"key"`
			Title     string `json:"title"`
		} `json:"entities"`
		Approval []string `json:"approval"`
	}
	path := "/v1/actions/" + url.PathEscape(action) + "/targets?" + url.Values{"for": {email}}.Encode()
	if err := c.do(ctx, http.MethodGet, path, nil, &res); err != nil {
		return nil, err
	}
	waits := make(map[string]bool, len(res.Approval))
	for _, id := range res.Approval {
		waits[id] = true
	}
	out := make([]Target, 0, len(res.Entities))
	for _, e := range res.Entities {
		ref := EntityRef{Blueprint: e.Blueprint, Key: e.Key}
		out = append(out, Target{EntityRef: ref, Title: e.Title, Waits: waits[ref.ID()]})
	}
	return out, nil
}

// Lend says whose request a tool call serves: the person, the action they
// asked in, the entity the request is on, and, when a run was granted more
// tiers, that run. The token's role must hold tools.lend.
type Lend struct {
	Person string    `json:"person"`
	Action string    `json:"action"`
	Entity EntityRef `json:"entity"`
	Run    string    `json:"run,omitempty"`
}

// Tool is one tool a caller may reach on an entity.
type Tool struct {
	ID          string         `json:"id"`
	Title       string         `json:"title"`
	Description string         `json:"description,omitempty"`
	Tier        string         `json:"tier"`
	Inputs      map[string]any `json:"inputs"`
}

// Tools lists the tools the caller holds the tier for on an entity. With
// lend, it lists what a call lent to that person's request may reach.
func (c *Client) Tools(ctx context.Context, entity EntityRef, lend *Lend) ([]Tool, error) {
	q := url.Values{"entity": {entity.ID()}}
	if lend != nil {
		q.Set("for", lend.Person)
		q.Set("action", lend.Action)
		if lend.Run != "" {
			q.Set("run", lend.Run)
		}
	}
	var res struct {
		Tools []Tool `json:"tools"`
	}
	if err := c.do(ctx, http.MethodGet, "/v1/tools?"+q.Encode(), nil, &res); err != nil {
		return nil, err
	}
	return res.Tools, nil
}

// ToolCall is one call of a tool.
type ToolCall struct {
	Entity *EntityRef     `json:"entity,omitempty"`
	Inputs map[string]any `json:"inputs,omitempty"`
	// As lends the call to a person's request.
	As *Lend `json:"as,omitempty"`
}

// ToolAnswer is what a tool call came to.
type ToolAnswer struct {
	// Run is the run the hub kept of the call.
	Run     string `json:"run"`
	State   string `json:"state"`
	Summary string `json:"summary"`
	// Text is what the tool printed, its head kept when Truncated.
	Text      string         `json:"text"`
	Truncated bool           `json:"truncated"`
	Outputs   map[string]any `json:"outputs"`
}

// CallTool calls a tool and waits for its answer, as long as the context
// allows. The call is sent once: a tool that changes something never runs
// twice for one call. A 403 *APIError means the tier is not held; a 409,
// that no runner serves the tool there.
func (c *Client) CallTool(ctx context.Context, id string, call ToolCall) (*ToolAnswer, error) {
	var answer ToolAnswer
	if err := c.doOnce(ctx, http.MethodPost, "/v1/tools/"+url.PathEscape(id)+"/call", call, &answer); err != nil {
		return nil, err
	}
	return &answer, nil
}
