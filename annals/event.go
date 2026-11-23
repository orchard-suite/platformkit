// Package annals is Annals' event contract in Go and a client
// that posts to it. An event is what an actor did, to what, with whom,
// with what authority, and how it went; Annals keeps each one as a
// record. Annals publishes the contract as prose
// (docs/event.md) and as a JSON Schema (schema/event.json); this package
// is the same contract, so a sender that validates here is held to the
// rules Annals applies.
package annals

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Event is one thing an actor did. ID and ReceivedAt are Annals'; a
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
	Cause      string            `json:"cause,omitempty"`
	Parties    []Party           `json:"parties,omitempty"`
	Items      []Item            `json:"items,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
}

type Actor struct {
	Kind       string `json:"kind"`
	ID         string `json:"id"`
	Name       string `json:"name,omitempty"`
	Email      string `json:"email,omitempty"`
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

// Party is someone or something else in the action besides the actor: who
// a site was shared with, who was added to a group, who approved.
type Party struct {
	Role     string `json:"role"`
	Kind     string `json:"kind"`
	ID       string `json:"id,omitempty"`
	Email    string `json:"email,omitempty"`
	Name     string `json:"name,omitempty"`
	External bool   `json:"external,omitempty"`
}

// Item is a thing the action touched besides its target, such as a file an
// agent read to answer a prompt. It names the thing, never what is in it.
type Item struct {
	System string `json:"system"`
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Title  string `json:"title,omitempty"`
	Access string `json:"access,omitempty"`
	Label  string `json:"label,omitempty"`
	Hash   string `json:"hash,omitempty"`
}

type Change struct {
	BeforeHash string `json:"before_hash,omitempty"`
	AfterHash  string `json:"after_hash,omitempty"`
	Summary    string `json:"summary,omitempty"`
}

// The closed lists. A value outside one is refused.
var (
	ActorKinds = []string{"agent", "person", "system"}
	Runtimes   = []string{"ion", "claude-code", "codex", "copilot", "other"}
	Outcomes   = []string{"ok", "refused", "failed"}
	PartyKinds = []string{"person", "group", "agent", "system", "anyone"}
)

// PersonKind is the target and party kind that names a person. What names
// a person is shown only to those allowed to see people.
const PersonKind = "person"

// Limits keep Annals a record of who did what to what, never a dump of
// content.
const (
	MaxShort    = 256  // ids, names, emails, verbs, kinds, systems, keys
	MaxTitle    = 512  // titles, change.summary, action.reason
	MaxLabels   = 16   // labels per event
	MaxLabelLen = 64   // a label key or value
	MaxParties  = 50   // parties per event
	MaxItems    = 100  // items per event
	MaxBatch    = 1000 // events in one POST
)

// ulidRe is the 26 Crockford base32 characters of a ULID.
var ulidRe = regexp.MustCompile(`^[0-7][0-9A-HJKMNP-TV-Z]{25}$`)

// emailRe finds an email address. One belongs only in an email field,
// where Annals can hide it from those not allowed to see people.
var emailRe = regexp.MustCompile(`[^\s@<>"']+@[^\s@<>"']+\.[A-Za-z]{2,}`)

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
		return refuse("id_set", "id is assigned by Annals")
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
	if err := short("cause", e.Cause, false); err != nil {
		return err
	}
	if e.Cause != "" && e.Cause == e.EventKey {
		return refuse("bad_cause", "an event cannot cause itself")
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
	if len(e.Parties) > MaxParties {
		return refuse("too_many_parties", "at most %d parties", MaxParties)
	}
	for i := range e.Parties {
		if err := e.Parties[i].validate(); err != nil {
			return err
		}
	}
	if len(e.Items) > MaxItems {
		return refuse("too_many_items", "at most %d items", MaxItems)
	}
	for i := range e.Items {
		if err := e.Items[i].validate(); err != nil {
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
		if err := noEmail("labels."+k, v); err != nil {
			return err
		}
	}
	return nil
}

func (p *Party) validate() error {
	if err := slug("parties.role", p.Role); err != nil {
		return err
	}
	if !oneOf(p.Kind, PartyKinds) {
		return refuse("bad_party_kind", "parties.kind must be one of %s", strings.Join(PartyKinds, ", "))
	}
	if err := short("parties.id", p.ID, p.Kind != "anyone"); err != nil {
		return err
	}
	if err := short("parties.name", p.Name, false); err != nil {
		return err
	}
	return email("parties.email", p.Email)
}

func (it *Item) validate() error {
	if err := slug("items.system", it.System); err != nil {
		return err
	}
	if err := slug("items.kind", it.Kind); err != nil {
		return err
	}
	if err := short("items.id", it.ID, true); err != nil {
		return err
	}
	if err := title("items.title", it.Title); err != nil {
		return err
	}
	if it.Access != "" {
		if err := slug("items.access", it.Access); err != nil {
			return err
		}
	}
	if err := short("items.label", it.Label, false); err != nil {
		return err
	}
	return short("items.hash", it.Hash, false)
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
	if err := email("actor.email", a.Email); err != nil {
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
	if err := title("action.target.title", a.Target.Title); err != nil {
		return err
	}
	if !oneOf(a.Outcome, Outcomes) {
		return refuse("bad_outcome", "action.outcome must be one of %s", strings.Join(Outcomes, ", "))
	}
	if err := title("action.reason", a.Reason); err != nil {
		return err
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
	return title("change.summary", c.Summary)
}

// title checks free text people read: a title, a summary, or a reason. It
// holds no email address; a person goes in a person field.
func title(name, v string) error {
	if len(v) > MaxTitle {
		return refuse("too_long", "%s is over %d characters", name, MaxTitle)
	}
	return noEmail(name, v)
}

func noEmail(name, v string) error {
	if emailRe.MatchString(v) {
		return refuse("email_in_text", "%s holds an email address; name people in actor, parties, or a person target", name)
	}
	return nil
}

func email(name, v string) error {
	if err := short(name, v, false); err != nil {
		return err
	}
	if v != "" && !emailRe.MatchString(v) {
		return refuse("bad_email", "%s must be an email address", name)
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
