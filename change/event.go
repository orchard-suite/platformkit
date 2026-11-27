package change

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// The five change events. Each is saved with the change that made it and
// carried on the estate's broker; Annals keeps them as records.
const (
	EventStated   = "change.stated"   // the runner was asked, with the statement
	EventRouted   = "change.routed"   // the judge's route, and its reasons
	EventDecided  = "change.decided"  // one yes or no
	EventEnded    = "change.ended"    // the change ran, with the applier's check
	EventObserved = "change.observed" // what the watch saw, and what it could not
)

// Event is one of the five change events.
type Event interface {
	Head() *Header
	// Validate checks the event against the contract. Kinds is the
	// estate's effect kinds; nil is the base set alone.
	Validate(kinds *Kinds) error
}

// Header is what every change event carries.
type Header struct {
	// Event is the event's name. Validate fills it when it is empty.
	Event string `json:"event"`
	// Sender is the member that sent the event, and Key its own key for
	// it: the same key from the same sender is stored once.
	Sender string    `json:"sender"`
	Key    string    `json:"key"`
	At     time.Time `json:"at"`
	// Of names the change.stated event this one belongs to, by its sender
	// and key, so a later event from any sender joins the change. A
	// change.stated event has none.
	Of *EventRef `json:"of,omitempty"`
	// Digest is the statement's digest.
	Digest string `json:"digest"`
}

// EventRef names an event by its sender and the sender's key.
type EventRef struct {
	Sender string `json:"sender"`
	Key    string `json:"key"`
}

func (h *Header) Head() *Header { return h }

// Validate checks a reference to an event read on its own, such as the
// record a change names outside its events; name is how a refusal names
// the field.
func (r EventRef) Validate(name string) error {
	if err := slug(name+".sender", r.Sender); err != nil {
		return err
	}
	return plain(name+".key", r.Key, true)
}

func (h *Header) validate(name string) error {
	if h.Event == "" {
		h.Event = name
	}
	if h.Event != name {
		return refuse("event_mismatch", "a %s event says it is %q", name, h.Event)
	}
	if err := slug("sender", h.Sender); err != nil {
		return err
	}
	if err := plain("key", h.Key, true); err != nil {
		return err
	}
	if h.At.IsZero() {
		return refuse("missing_at", "at is required, RFC 3339")
	}
	if err := digest("digest", h.Digest); err != nil {
		return err
	}
	if name == EventStated {
		if h.Of != nil {
			return refuse("bad_of", "a change.stated event starts the change, so it names no other")
		}
		return nil
	}
	if h.Of == nil {
		return refuse("missing_of", "a %s event names the change.stated event it belongs to", name)
	}
	if err := h.Of.Validate("of"); err != nil {
		return err
	}
	if h.Of.Sender == h.Sender && h.Of.Key == h.Key {
		return refuse("bad_of", "an event cannot belong to itself")
	}
	return nil
}

// Stated is sent by the runner when it is asked for a change.
type Stated struct {
	Header
	Statement Statement `json:"statement"`
}

func (e *Stated) Validate(kinds *Kinds) error {
	if err := e.Header.validate(EventStated); err != nil {
		return err
	}
	if err := e.Statement.Validate(kinds); err != nil {
		return err
	}
	d, err := e.Statement.Digest()
	if err != nil {
		return err
	}
	if d != e.Digest {
		return refuse("digest_mismatch", "digest is %s, the statement's is %s", e.Digest, d)
	}
	return nil
}

// Route is the judge's answer: no one, or the people who must look.
type Route struct {
	// To is RouteNoOne or RoutePeople.
	To string `json:"to"`
	// Grants are who may decide, in the runner's own terms for approvers.
	Grants []Grant `json:"grants,omitempty"`
	// Quorum is how many must agree.
	Quorum int `json:"quorum,omitempty"`
	// By is when the decision is due.
	By *time.Time `json:"by,omitempty"`
	// Start is the earliest the change may start.
	Start *time.Time `json:"start,omitempty"`
}

// Grant names who may decide, as the runner names an approver: the
// values of a field, found on the entity or along a path from it.
type Grant struct {
	Path   string   `json:"path,omitempty"`
	Field  string   `json:"field,omitempty"`
	Values []string `json:"values"`
}

// RouteReason is why the route came: an effect, an unknown part, a fence,
// or a rule about the change as a whole, such as a runner's own approval
// rule before a judge answers for it.
type RouteReason struct {
	// Effect is the index of the statement's effect it is about.
	Effect *int `json:"effect,omitempty"`
	// Part is the index of the statement's unknown part it is about.
	Part *int `json:"part,omitempty"`
	// Fence is the fence that sent the change to people, when one did.
	Fence string `json:"fence,omitempty"`
	// Rule names the rule that answered.
	Rule string `json:"rule,omitempty"`
	// Proof is the level of proof the judge held for the effect.
	Proof string `json:"proof,omitempty"`
}

// Draft is what a pack that does not count yet would have answered.
type Draft struct {
	Pack    string        `json:"pack"`
	Route   Route         `json:"route"`
	Reasons []RouteReason `json:"reasons,omitempty"`
}

// Judge names who judged.
type Judge struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Pack    string `json:"pack"`
}

// Routed is sent by the runner with the judge's route. When the judge did
// not answer, Judge is nil and the route is to people, by FenceJudgeDown.
type Routed struct {
	Header
	Judge   *Judge        `json:"judge,omitempty"`
	Route   Route         `json:"route"`
	Reasons []RouteReason `json:"reasons,omitempty"`
	Drafts  []Draft       `json:"drafts,omitempty"`
}

// The routes, the fences, and the levels of proof.
const (
	RouteNoOne  = "no_one"
	RoutePeople = "people"

	FenceUnknown   = "unknown"    // an effect with no proof, or a part the writer could not name
	FenceAsker     = "asker"      // a route of no one for a change applied where it was asked
	FenceJudgeDown = "judge_down" // the judge did not answer

	ProofUnknown   = "unknown"
	ProofClaimed   = "claimed"
	ProofRehearsed = "rehearsed"
	ProofMeasured  = "measured"
)

var (
	routes = []string{RouteNoOne, RoutePeople}
	fences = []string{FenceUnknown, FenceAsker, FenceJudgeDown}
	proofs = []string{ProofUnknown, ProofClaimed, ProofRehearsed, ProofMeasured}
)

// Limits on what a route and its reasons carry.
const (
	MaxGrants  = 16
	MaxValues  = 50
	MaxReasons = MaxEffects + MaxUnknown + 3
	MaxDrafts  = 8
)

func (e *Routed) Validate(_ *Kinds) error {
	if err := e.Header.validate(EventRouted); err != nil {
		return err
	}
	if e.Judge == nil {
		if e.Route.To != RoutePeople || !hasFence(e.Reasons, FenceJudgeDown) {
			return refuse("missing_judge", "a route with no judge goes to people, by the judge_down fence")
		}
	} else {
		if err := slug("judge.name", e.Judge.Name); err != nil {
			return err
		}
		if err := plain("judge.version", e.Judge.Version, true); err != nil {
			return err
		}
		if err := plain("judge.pack", e.Judge.Pack, true); err != nil {
			return err
		}
	}
	if err := validateRoute("route", e.Route, e.Reasons); err != nil {
		return err
	}
	if len(e.Drafts) > MaxDrafts {
		return refuse("too_many_drafts", "at most %d drafts", MaxDrafts)
	}
	for i, d := range e.Drafts {
		name := fmt.Sprintf("drafts[%d]", i)
		if err := plain(name+".pack", d.Pack, true); err != nil {
			return err
		}
		if err := validateRoute(name+".route", d.Route, d.Reasons); err != nil {
			return err
		}
	}
	return nil
}

func validateRoute(name string, r Route, reasons []RouteReason) error {
	if !oneOf(r.To, routes) {
		return refuse("bad_route", "%s.to must be one of %s", name, strings.Join(routes, ", "))
	}
	if r.To == RouteNoOne {
		if len(r.Grants) > 0 || r.Quorum != 0 || r.By != nil || r.Start != nil {
			return refuse("bad_route", "%s: a route of no one is queued at once, so it names no one, no quorum, and no time", name)
		}
	} else {
		if len(r.Grants) == 0 {
			return refuse("missing_grants", "%s: a route to people names who may decide", name)
		}
		if r.Quorum < 1 {
			return refuse("bad_quorum", "%s.quorum is at least 1", name)
		}
	}
	if len(r.Grants) > MaxGrants {
		return refuse("too_many_grants", "at most %d grants", MaxGrants)
	}
	for i, g := range r.Grants {
		gn := fmt.Sprintf("%s.grants[%d]", name, i)
		if err := plain(gn+".path", g.Path, false); err != nil {
			return err
		}
		if err := plain(gn+".field", g.Field, false); err != nil {
			return err
		}
		if len(g.Values) == 0 || len(g.Values) > MaxValues {
			return refuse("bad_grant", "%s.values holds 1 to %d values", gn, MaxValues)
		}
		for _, v := range g.Values {
			if err := person(gn+".values", v, true); err != nil {
				return err
			}
		}
	}
	if len(reasons) > MaxReasons {
		return refuse("too_many_reasons", "at most %d reasons", MaxReasons)
	}
	for i, re := range reasons {
		rn := fmt.Sprintf("reasons[%d]", i)
		if re.Effect == nil && re.Part == nil && re.Fence == "" && re.Rule == "" {
			return refuse("empty_reason", "%s names an effect, an unknown part, a fence, or a rule", rn)
		}
		if (re.Effect != nil && *re.Effect < 0) || (re.Part != nil && *re.Part < 0) {
			return refuse("bad_reason", "%s: an index is never below zero", rn)
		}
		if re.Fence != "" && !oneOf(re.Fence, fences) {
			return refuse("bad_fence", "%s.fence must be one of %s", rn, strings.Join(fences, ", "))
		}
		if re.Proof != "" && !oneOf(re.Proof, proofs) {
			return refuse("bad_proof", "%s.proof must be one of %s", rn, strings.Join(proofs, ", "))
		}
		if err := plain(rn+".rule", re.Rule, false); err != nil {
			return err
		}
	}
	if r.To == RouteNoOne && (hasFence(reasons, FenceUnknown) || hasFence(reasons, FenceAsker) || hasFence(reasons, FenceJudgeDown)) {
		return refuse("fence_ignored", "%s: a fence sends the change to people, never to no one", name)
	}
	return nil
}

func hasFence(reasons []RouteReason, fence string) bool {
	for _, r := range reasons {
		if r.Fence == fence {
			return true
		}
	}
	return false
}

// Decided is sent by the runner for each yes or no.
type Decided struct {
	Header
	By Actor `json:"by"`
	// Answer is AnswerYes or AnswerNo.
	Answer string `json:"answer"`
}

const (
	AnswerYes = "yes"
	AnswerNo  = "no"
)

func (e *Decided) Validate(_ *Kinds) error {
	if err := e.Header.validate(EventDecided); err != nil {
		return err
	}
	if err := e.By.validate("by"); err != nil {
		return err
	}
	if e.Answer != AnswerYes && e.Answer != AnswerNo {
		return refuse("bad_answer", "answer must be %s or %s", AnswerYes, AnswerNo)
	}
	return nil
}

// Ended is sent by the runner when the change has run, or was refused,
// with the applier's check of the statement.
type Ended struct {
	Header
	// Outcome is OutcomeOK, OutcomeFailed, or OutcomeRefused.
	Outcome string `json:"outcome"`
	// Check is CheckMatched, CheckDiffered, or CheckUnchecked.
	Check string `json:"check"`
	// Differs names the statement's fields that differed, from Diff.
	Differs []string `json:"differs,omitempty"`
}

const (
	OutcomeOK      = "ok"
	OutcomeFailed  = "failed"
	OutcomeRefused = "refused"

	CheckMatched   = "matched"   // the recomputed statement had the judged digest
	CheckDiffered  = "differed"  // it did not, and the applier refused
	CheckUnchecked = "unchecked" // the applier could not recompute it
)

var (
	outcomes        = []string{OutcomeOK, OutcomeFailed, OutcomeRefused}
	checks          = []string{CheckMatched, CheckDiffered, CheckUnchecked}
	statementFields = []string{"key", "writer", "actor", "things", "artifact", "applier", "effects", "unknown"}
)

func (e *Ended) Validate(_ *Kinds) error {
	if err := e.Header.validate(EventEnded); err != nil {
		return err
	}
	if !oneOf(e.Outcome, outcomes) {
		return refuse("bad_outcome", "outcome must be one of %s", strings.Join(outcomes, ", "))
	}
	if !oneOf(e.Check, checks) {
		return refuse("bad_check", "check must be one of %s", strings.Join(checks, ", "))
	}
	if e.Check == CheckDiffered {
		if len(e.Differs) == 0 {
			return refuse("missing_differs", "a statement that differed names what differed")
		}
		if e.Outcome != OutcomeRefused {
			return refuse("ran_differed", "a statement that differed from the one judged is refused, never run")
		}
	} else if len(e.Differs) > 0 {
		return refuse("bad_differs", "differs is only for a statement that differed")
	}
	for _, d := range e.Differs {
		if !oneOf(d, statementFields) {
			return refuse("bad_differs", "differs names a statement field, one of %s", strings.Join(statementFields, ", "))
		}
	}
	return nil
}

// Observed is sent by the watch when a change's window closes: what each
// subject did, which kinds it could not see, and the incidents joined.
type Observed struct {
	Header
	Watch     Watcher    `json:"watch"`
	From      time.Time  `json:"from"`
	To        time.Time  `json:"to"`
	Sights    []Sight    `json:"sights,omitempty"`
	Incidents []Incident `json:"incidents,omitempty"`
}

// Watcher names the watch.
type Watcher struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Sight is what the watch saw for one effect kind on one subject, by the
// path the change took to it. Proof is counted by kind, the subject's
// class, and the path.
type Sight struct {
	Kind    string `json:"kind"`
	Subject Ref    `json:"subject"`
	Path    string `json:"path"`
	// Verdict is VerdictClean, VerdictHarm, or VerdictBlind.
	Verdict string `json:"verdict"`
	// Signals names the signals it read, never their values.
	Signals []string `json:"signals,omitempty"`
}

// Incident is one incident joined to the change.
type Incident struct {
	System string `json:"system"`
	ID     string `json:"id"`
}

const (
	VerdictClean = "clean" // it looked, and nothing happened
	VerdictHarm  = "harm"  // it looked, and the thing was hurt
	VerdictBlind = "blind" // it could not look; never the same as clean
)

var verdicts = []string{VerdictClean, VerdictHarm, VerdictBlind}

// Limits on what an observation carries.
const (
	MaxSights    = 2000
	MaxSignals   = 32
	MaxIncidents = 100
)

func (e *Observed) Validate(kinds *Kinds) error {
	if kinds == nil {
		kinds = Base()
	}
	if err := e.Header.validate(EventObserved); err != nil {
		return err
	}
	if err := slug("watch.name", e.Watch.Name); err != nil {
		return err
	}
	if err := plain("watch.version", e.Watch.Version, true); err != nil {
		return err
	}
	if e.From.IsZero() || e.To.IsZero() || e.To.Before(e.From) {
		return refuse("bad_window", "from and to are required, and to is never before from")
	}
	if len(e.Sights) > MaxSights {
		return refuse("too_many_sights", "at most %d sights", MaxSights)
	}
	for i, s := range e.Sights {
		name := fmt.Sprintf("sights[%d]", i)
		if _, ok := kinds.Get(s.Kind); !ok {
			return refuse("unknown_kind", "%s.kind %q is not a registered effect kind", name, s.Kind)
		}
		if err := s.Subject.validate(name + ".subject"); err != nil {
			return err
		}
		if err := slug(name+".path", s.Path); err != nil {
			return err
		}
		if !oneOf(s.Verdict, verdicts) {
			return refuse("bad_verdict", "%s.verdict must be one of %s", name, strings.Join(verdicts, ", "))
		}
		if len(s.Signals) > MaxSignals {
			return refuse("too_many_signals", "%s: at most %d signals", name, MaxSignals)
		}
		for _, sig := range s.Signals {
			if err := plain(name+".signals", sig, true); err != nil {
				return err
			}
		}
	}
	if len(e.Incidents) > MaxIncidents {
		return refuse("too_many_incidents", "at most %d incidents", MaxIncidents)
	}
	for i, inc := range e.Incidents {
		name := fmt.Sprintf("incidents[%d]", i)
		if err := slug(name+".system", inc.System); err != nil {
			return err
		}
		if err := plain(name+".id", inc.ID, true); err != nil {
			return err
		}
	}
	return nil
}

// Decode reads one change event, of whichever of the five it names. It
// refuses a field the contract does not have; it does not validate.
func Decode(b []byte) (Event, error) {
	var head struct {
		Event string `json:"event"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return nil, fmt.Errorf("change: decode event: %w", err)
	}
	var e Event
	switch head.Event {
	case EventStated:
		e = &Stated{}
	case EventRouted:
		e = &Routed{}
	case EventDecided:
		e = &Decided{}
	case EventEnded:
		e = &Ended{}
	case EventObserved:
		e = &Observed{}
	default:
		return nil, refuse("unknown_event", "event %q is not a change event", head.Event)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(e); err != nil {
		return nil, fmt.Errorf("change: decode %s: %w", head.Event, err)
	}
	return e, nil
}
