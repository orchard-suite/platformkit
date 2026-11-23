// Package ledger is the agent ledger's event contract in Go and a client
// that posts to it. An event is what an actor did, to what, with what
// authority, and how it went. The ledger publishes the contract as prose
// (docs/event.md) and as a JSON Schema (schema/event.json); this package
// is the same contract, so a sender that validates here is held to the
// rules the ledger applies.
package ledger

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Event is one thing an actor did. ID and ReceivedAt are the ledger's; a
// sender fills the rest.
type Event struct {
	ID         string            `json:"id,omitempty"`
	At         time.Time         `json:"at"`
	ReceivedAt time.Time         `json:"received_at,omitzero"`
	Source     string            `json:"source"`
	EventKey   string            `json:"event_key,omitempty"`
	Actor      Actor             `json:"actor"`
	Agent      *Agent            `json:"agent,omitempty"`
	Authority  *Authority        `json:"authority,omitempty"`
	Action     Action            `json:"action"`
	Change     *Change           `json:"change,omitempty"`
	TraceID    string            `json:"trace_id,omitempty"`
	Corrects   string            `json:"corrects,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
}

type Actor struct {
	Kind       string `json:"kind"`
	ID         string `json:"id"`
	Name       string `json:"name,omitempty"`
	OnBehalfOf string `json:"on_behalf_of,omitempty"`
}

type Agent struct {
	Runtime   string `json:"runtime"`
	Model     string `json:"model,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	Harness   string `json:"harness,omitempty"`
}

type Authority struct {
	TokenID   string    `json:"token_id,omitempty"`
	Scope     []string  `json:"scope,omitempty"`
	GrantedBy string    `json:"granted_by,omitempty"`
	Approval  *Approval `json:"approval,omitempty"`
}

type Approval struct {
	Required bool      `json:"required"`
	By       string    `json:"by,omitempty"`
	At       time.Time `json:"at,omitzero"`
}

type Action struct {
	Verb    string `json:"verb"`
	Target  Target `json:"target"`
	Outcome string `json:"outcome"`
	Reason  string `json:"reason,omitempty"`
}

type Target struct {
	System string `json:"system"`
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Title  string `json:"title,omitempty"`
}

type Change struct {
	BeforeHash string `json:"before_hash,omitempty"`
	AfterHash  string `json:"after_hash,omitempty"`
	Summary    string `json:"summary,omitempty"`
}

// The closed lists. A value outside one is refused.
var (
	ActorKinds = []string{"agent", "person", "system"}
	Runtimes   = []string{"ion", "claude-code", "codex", "other"}
	Outcomes   = []string{"ok", "refused", "failed"}
)

// Limits keep the ledger a record of shapes and ids, never a dump of values.
const (
	MaxShort    = 256  // ids, names, verbs, kinds, systems, keys
	MaxTitle    = 512  // target.title, change.summary, action.reason
	MaxLabels   = 16   // labels per event
	MaxLabelLen = 64   // a label key or value
	MaxBatch    = 1000 // events in one POST
)

// ulidRe is the 26 Crockford base32 characters of a ULID.
var ulidRe = regexp.MustCompile(`^[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

// slugRe is a source, verb, system, kind, runtime, or label key: lowercase
// words joined by dots, dashes, or underscores.
var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)

// ErrInvalid wraps every refusal, with Reason naming the first rule broken.
type ErrInvalid struct {
	Reason string
	Detail string
}

func (e *ErrInvalid) Error() string { return e.Reason + ": " + e.Detail }

func refuse(reason, format string, args ...any) error {
	return &ErrInvalid{Reason: reason, Detail: fmt.Sprintf(format, args...)}
}

// Reason returns the refusal reason of err, or "" when err is not a refusal.
func Reason(err error) string {
	var inv *ErrInvalid
	if errors.As(err, &inv) {
		return inv.Reason
	}
	return ""
}

// Validate checks an event a sender posted. source is the source the
// sender's token allows; the event must claim that source or none.
func (e *Event) Validate(source string) error {
	if e.Source == "" {
		e.Source = source
	}
	if e.Source != source {
		return refuse("source_mismatch", "the token is for source %q, the event says %q", source, e.Source)
	}
	if e.At.IsZero() {
		return refuse("missing_at", "at is required, RFC 3339")
	}
	if e.ID != "" {
		return refuse("id_set", "id is assigned by the ledger")
	}
	if e.Corrects != "" && !ulidRe.MatchString(e.Corrects) {
		return refuse("bad_corrects", "corrects must be the ULID of an earlier event")
	}
	if err := short("event_key", e.EventKey, false); err != nil {
		return err
	}
	if err := short("trace_id", e.TraceID, false); err != nil {
		return err
	}
	if err := e.Actor.validate(); err != nil {
		return err
	}
	if e.Agent != nil {
		if err := e.Agent.validate(); err != nil {
			return err
		}
	}
	if e.Authority != nil {
		if err := e.Authority.validate(); err != nil {
			return err
		}
	}
	if err := e.Action.validate(); err != nil {
		return err
	}
	if e.Change != nil {
		if err := e.Change.validate(); err != nil {
			return err
		}
	}
	if len(e.Labels) > MaxLabels {
		return refuse("too_many_labels", "at most %d labels", MaxLabels)
	}
	for k, v := range e.Labels {
		if !slugRe.MatchString(k) || len(k) > MaxLabelLen {
			return refuse("bad_label", "label key %q must be a lowercase slug under %d characters", k, MaxLabelLen)
		}
		if len(v) > MaxLabelLen || strings.ContainsAny(v, "\n\r\x00") {
			return refuse("bad_label", "label %q value must be under %d characters with no control characters", k, MaxLabelLen)
		}
	}
	return nil
}

func (a *Actor) validate() error {
	if !oneOf(a.Kind, ActorKinds) {
		return refuse("bad_actor_kind", "actor.kind must be one of %s", strings.Join(ActorKinds, ", "))
	}
	if err := short("actor.id", a.ID, true); err != nil {
		return err
	}
	if err := short("actor.name", a.Name, false); err != nil {
		return err
	}
	if err := short("actor.on_behalf_of", a.OnBehalfOf, false); err != nil {
		return err
	}
	if a.OnBehalfOf != "" && a.Kind == "person" {
		return refuse("bad_on_behalf_of", "a person acts for nobody; on_behalf_of is for agents and systems")
	}
	return nil
}

func (a *Agent) validate() error {
	if !oneOf(a.Runtime, Runtimes) {
		return refuse("bad_runtime", "agent.runtime must be one of %s", strings.Join(Runtimes, ", "))
	}
	for _, f := range []struct{ name, v string }{{"agent.model", a.Model}, {"agent.session_id", a.SessionID}, {"agent.harness", a.Harness}} {
		if err := short(f.name, f.v, false); err != nil {
			return err
		}
	}
	return nil
}

func (a *Authority) validate() error {
	if err := short("authority.token_id", a.TokenID, false); err != nil {
		return err
	}
	if err := short("authority.granted_by", a.GrantedBy, false); err != nil {
		return err
	}
	if len(a.Scope) > MaxLabels {
		return refuse("too_many_scopes", "at most %d scopes", MaxLabels)
	}
	for _, s := range a.Scope {
		if err := short("authority.scope", s, true); err != nil {
			return err
		}
	}
	if a.Approval != nil {
		if err := short("authority.approval.by", a.Approval.By, false); err != nil {
			return err
		}
		if a.Approval.Required && a.Approval.By == "" && !a.Approval.At.IsZero() {
			return refuse("bad_approval", "an approval with a time names who gave it")
		}
	}
	return nil
}

func (a *Action) validate() error {
	if err := slug("action.verb", a.Verb); err != nil {
		return err
	}
	if err := slug("action.target.system", a.Target.System); err != nil {
		return err
	}
	if err := slug("action.target.kind", a.Target.Kind); err != nil {
		return err
	}
	if err := short("action.target.id", a.Target.ID, true); err != nil {
		return err
	}
	if len(a.Target.Title) > MaxTitle {
		return refuse("too_long", "action.target.title is over %d characters", MaxTitle)
	}
	if !oneOf(a.Outcome, Outcomes) {
		return refuse("bad_outcome", "action.outcome must be one of %s", strings.Join(Outcomes, ", "))
	}
	if len(a.Reason) > MaxTitle {
		return refuse("too_long", "action.reason is over %d characters", MaxTitle)
	}
	if a.Outcome != "ok" && a.Reason == "" {
		return refuse("missing_reason", "a %s outcome says why in action.reason", a.Outcome)
	}
	return nil
}

func (c *Change) validate() error {
	for _, f := range []struct{ name, v string }{{"change.before_hash", c.BeforeHash}, {"change.after_hash", c.AfterHash}} {
		if err := short(f.name, f.v, false); err != nil {
			return err
		}
	}
	if len(c.Summary) > MaxTitle {
		return refuse("too_long", "change.summary is over %d characters", MaxTitle)
	}
	return nil
}

func short(name, v string, required bool) error {
	if v == "" {
		if required {
			return refuse("missing_"+strings.ReplaceAll(name, ".", "_"), "%s is required", name)
		}
		return nil
	}
	if len(v) > MaxShort {
		return refuse("too_long", "%s is over %d characters", name, MaxShort)
	}
	if strings.ContainsAny(v, "\n\r\x00") {
		return refuse("bad_text", "%s holds a control character", name)
	}
	return nil
}

func slug(name, v string) error {
	if v == "" {
		return refuse("missing_"+strings.ReplaceAll(name, ".", "_"), "%s is required", name)
	}
	if len(v) > MaxShort || !slugRe.MatchString(v) {
		return refuse("bad_slug", "%s must be a lowercase slug such as %q", name, "file.write")
	}
	return nil
}

func oneOf(v string, list []string) bool {
	for _, x := range list {
		if v == x {
			return true
		}
	}
	return false
}
