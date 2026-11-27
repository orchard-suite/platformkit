// Package change is the change statement: what one change to a live thing
// will do, stated before it runs, in one shape for every tool and every
// estate. A live thing is anything in the catalog that people or agents
// use while it runs. A tool that changes one writes a statement; the
// estate's judge reads it and answers a route; the runner keeps the route
// and the events record what happened.
//
// The contract is published three ways: the suite's page "Changes to live
// things" in words, schema/ as JSON Schema for every language, and this
// package in Go. The contract test holds the schema and this validator to
// the same worked examples in schema/examples.
//
// A statement is data, never content: names, kinds, and references. It
// holds no value of a setting, no secret, and no message body.
package change

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Statement is one change, stated by the tool that will make it.
type Statement struct {
	// Key is the writer's own key for the change, stored once.
	Key    string `json:"key"`
	Writer Writer `json:"writer"`
	Actor  Actor  `json:"actor"`
	// Things are the catalog entities the change touches.
	Things   []Ref    `json:"things,omitempty"`
	Artifact Artifact `json:"artifact"`
	// Applier is where the change runs: ApplierHub or ApplierAsker.
	Applier string   `json:"applier"`
	Effects []Effect `json:"effects,omitempty"`
	// Unknown is what the writer saw change and could not name an effect
	// for, or could not read. A writer never leaves a part out to pass.
	Unknown []Unknown `json:"unknown,omitempty"`
}

// Writer is the tool that wrote the statement.
type Writer struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Actor is who asks for the change. An agent or a system may act for a
// person, named in OnBehalfOf.
type Actor struct {
	Kind       string `json:"kind"`
	ID         string `json:"id"`
	OnBehalfOf string `json:"on_behalf_of,omitempty"`
}

// Ref names one catalog entity by its blueprint and key.
type Ref struct {
	Blueprint string `json:"blueprint"`
	Key       string `json:"key"`
}

// Artifact is the exact thing that will run: a plan file, an image, a
// manifest. Its digest is what the applier recomputes the statement from.
type Artifact struct {
	Kind   string `json:"kind"`
	Digest string `json:"digest"`
}

// Effect is what the change does to one live thing, never the tool's verb.
type Effect struct {
	// Kind is a registered effect kind (Kinds).
	Kind string `json:"kind"`
	// Subject is the entity that feels it. It need not be one of Things:
	// a change to a plan's size stops the apps on it.
	Subject Ref `json:"subject"`
	// Path is how the change reaches the thing: PathInPlace, PathSwap,
	// PathRolling, or a path the estate names.
	Path string `json:"path"`
	// Size is how much, in the kind's unit, when the kind has one.
	Size *int64 `json:"size,omitempty"`
	// Undo says whether the old artifact puts it back: UndoYes, UndoNo, or
	// UndoPartial.
	Undo  string `json:"undo"`
	Claim Claim  `json:"claim"`
}

// Claim is where a predicted effect came from.
type Claim struct {
	// From is ClaimWriter, ClaimEstate, or ClaimModel.
	From string `json:"from"`
	// Rule names the row of the table that predicted it.
	Rule string `json:"rule,omitempty"`
	// Version is the provider version the prediction is true for.
	Version string `json:"version,omitempty"`
}

// Unknown is one part of the change the writer could not name an effect
// for. It routes the change to people.
type Unknown struct {
	Thing *Ref `json:"thing,omitempty"`
	// Part names the part that changes, such as a resource's attribute,
	// never its value.
	Part string `json:"part"`
	// Why is WhyUnread or WhyUnnamed.
	Why string `json:"why"`
}

// The closed lists of a statement. A value outside one is refused.
const (
	ApplierHub   = "hub"   // a job the hub starts
	ApplierAsker = "asker" // the machine that asked

	ActorPerson = "person"
	ActorAgent  = "agent"
	ActorSystem = "system"

	UndoYes     = "yes"
	UndoNo      = "no"
	UndoPartial = "partial"

	ClaimWriter = "writer" // the writer's own table
	ClaimEstate = "estate" // the estate's table
	ClaimModel  = "model"  // a model's reading of the provider's documents

	WhyUnread  = "unread"  // the writer could not read the part
	WhyUnnamed = "unnamed" // the writer read it and has no effect for it
)

// The paths the suite names. An estate may name its own.
const (
	PathInPlace = "in-place"
	PathSwap    = "swap"
	PathRolling = "rolling"
)

var (
	appliers   = []string{ApplierHub, ApplierAsker}
	actorKinds = []string{ActorPerson, ActorAgent, ActorSystem}
	undos      = []string{UndoYes, UndoNo, UndoPartial}
	claimFroms = []string{ClaimWriter, ClaimEstate, ClaimModel}
	whys       = []string{WhyUnread, WhyUnnamed}
)

// Limits keep a statement a list of names, never a copy of the change.
const (
	MaxShort   = 256  // every name, key, and version
	MaxText    = 512  // a kind's meaning
	MaxThings  = 1000 // things per statement
	MaxEffects = 1000 // effects per statement
	MaxUnknown = 1000 // unknown parts per statement
)

var (
	// slugRe is a writer, blueprint, kind, path, or sender: lowercase words
	// joined by dots, dashes, or underscores.
	slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	// digestRe is a digest: sha256 in lowercase hex.
	digestRe = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	emailRe  = regexp.MustCompile(`[^\s@<>"']+@[^\s@<>"']+\.[A-Za-z]{2,}`)
	// tokenRe finds a run that reads as a key or a token: long, with upper
	// case, lower case, and digits mixed, as base64 is.
	tokenRe = regexp.MustCompile(`[A-Za-z0-9+/]{32,}`)
	jwtRe   = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)
)

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

// Validate checks a statement against the contract and the estate's effect
// kinds. A nil kinds is the base set alone.
func (s *Statement) Validate(kinds *Kinds) error {
	if kinds == nil {
		kinds = Base()
	}
	if err := plain("key", s.Key, true); err != nil {
		return err
	}
	if err := slug("writer.name", s.Writer.Name); err != nil {
		return err
	}
	if err := plain("writer.version", s.Writer.Version, true); err != nil {
		return err
	}
	if err := s.Actor.validate("actor"); err != nil {
		return err
	}
	if len(s.Things) == 0 && len(s.Unknown) == 0 {
		return refuse("nothing_stated", "a statement names at least one thing it touches, or a part it could not name")
	}
	if len(s.Things) > MaxThings {
		return refuse("too_many_things", "at most %d things", MaxThings)
	}
	seen := make(map[Ref]bool, len(s.Things))
	for i, t := range s.Things {
		if err := t.validate(fmt.Sprintf("things[%d]", i)); err != nil {
			return err
		}
		if seen[t] {
			return refuse("duplicate_thing", "things names %s:%s twice", t.Blueprint, t.Key)
		}
		seen[t] = true
	}
	if err := slug("artifact.kind", s.Artifact.Kind); err != nil {
		return err
	}
	if err := digest("artifact.digest", s.Artifact.Digest); err != nil {
		return err
	}
	if !oneOf(s.Applier, appliers) {
		return refuse("bad_applier", "applier must be one of %s", strings.Join(appliers, ", "))
	}
	if len(s.Effects) > MaxEffects {
		return refuse("too_many_effects", "at most %d effects", MaxEffects)
	}
	for i := range s.Effects {
		if err := s.Effects[i].validate(fmt.Sprintf("effects[%d]", i), kinds); err != nil {
			return err
		}
	}
	if len(s.Unknown) > MaxUnknown {
		return refuse("too_many_unknown", "at most %d unknown parts", MaxUnknown)
	}
	for i := range s.Unknown {
		if err := s.Unknown[i].validate(fmt.Sprintf("unknown[%d]", i)); err != nil {
			return err
		}
	}
	return nil
}

func (a *Actor) validate(name string) error {
	if !oneOf(a.Kind, actorKinds) {
		return refuse("bad_actor_kind", "%s.kind must be one of %s", name, strings.Join(actorKinds, ", "))
	}
	if err := person(name+".id", a.ID, true); err != nil {
		return err
	}
	if err := person(name+".on_behalf_of", a.OnBehalfOf, false); err != nil {
		return err
	}
	if a.OnBehalfOf != "" && a.Kind == ActorPerson {
		return refuse("bad_on_behalf_of", "a person acts for nobody; on_behalf_of is for agents and systems")
	}
	return nil
}

func (r Ref) validate(name string) error {
	if err := slug(name+".blueprint", r.Blueprint); err != nil {
		return err
	}
	return plain(name+".key", r.Key, true)
}

func (e *Effect) validate(name string, kinds *Kinds) error {
	k, ok := kinds.Get(e.Kind)
	if !ok {
		return refuse("unknown_kind", "%s.kind %q is not a registered effect kind", name, e.Kind)
	}
	if err := e.Subject.validate(name + ".subject"); err != nil {
		return err
	}
	if err := slug(name+".path", e.Path); err != nil {
		return err
	}
	if e.Size != nil {
		if k.Unit == "" {
			return refuse("size_without_unit", "%s: the kind %q has no unit, so it has no size", name, k.Name)
		}
		if *e.Size < 0 {
			return refuse("bad_size", "%s.size is never below zero", name)
		}
	}
	if !oneOf(e.Undo, undos) {
		return refuse("bad_undo", "%s.undo must be one of %s", name, strings.Join(undos, ", "))
	}
	if !oneOf(e.Claim.From, claimFroms) {
		return refuse("bad_claim", "%s.claim.from must be one of %s", name, strings.Join(claimFroms, ", "))
	}
	if err := plain(name+".claim.rule", e.Claim.Rule, false); err != nil {
		return err
	}
	return plain(name+".claim.version", e.Claim.Version, false)
}

func (u *Unknown) validate(name string) error {
	if u.Thing != nil {
		if err := u.Thing.validate(name + ".thing"); err != nil {
			return err
		}
	}
	if err := plain(name+".part", u.Part, true); err != nil {
		return err
	}
	if !oneOf(u.Why, whys) {
		return refuse("bad_why", "%s.why must be one of %s", name, strings.Join(whys, ", "))
	}
	return nil
}

// plain checks a name, a key, or a version: short, one line, and never a
// value, a secret, or an address a person is reached at.
func plain(name, v string, required bool) error {
	if err := short(name, v, required); err != nil {
		return err
	}
	if emailRe.MatchString(v) {
		return refuse("email_in_name", "%s holds an email address; a person is named only as an actor or an approver", name)
	}
	return notValue(name, v)
}

// person checks a field that names a person or an agent. An email address
// may stand here and nowhere else.
func person(name, v string, required bool) error {
	if err := short(name, v, required); err != nil {
		return err
	}
	return notValue(name, v)
}

// notValue refuses what reads as content rather than a name: an
// assignment, a key, a token, or a certificate.
func notValue(name, v string) error {
	if strings.Contains(v, "=") {
		return refuse("value_in_name", "%s holds %q; a statement names a part, never what it is set to", name, "=")
	}
	if strings.Contains(v, "-----BEGIN") || jwtRe.MatchString(v) {
		return refuse("secret_in_name", "%s holds what reads as a key or a token", name)
	}
	for _, run := range tokenRe.FindAllString(v, -1) {
		if strings.ContainsAny(run, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") &&
			strings.ContainsAny(run, "abcdefghijklmnopqrstuvwxyz") &&
			strings.ContainsAny(run, "0123456789") {
			return refuse("secret_in_name", "%s holds what reads as a key or a token", name)
		}
	}
	return nil
}

func short(name, v string, required bool) error {
	if v == "" {
		if required {
			return refuse("missing_"+field(name), "%s is required", name)
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
		return refuse("missing_"+field(name), "%s is required", name)
	}
	if len(v) > MaxShort || !slugRe.MatchString(v) {
		return refuse("bad_slug", "%s must be a lowercase slug such as %q", name, "in-place")
	}
	return nil
}

func digest(name, v string) error {
	if !digestRe.MatchString(v) {
		return refuse("bad_digest", "%s must be sha256: and 64 lowercase hex characters", name)
	}
	return nil
}

// field turns a field's name, such as effects[2].claim.from, into the part
// of a reason: claim_from.
func field(name string) string {
	if i := strings.LastIndex(name, "]."); i >= 0 {
		name = name[i+2:]
	}
	return strings.ReplaceAll(name, ".", "_")
}

func oneOf(v string, list []string) bool {
	for _, x := range list {
		if v == x {
			return true
		}
	}
	return false
}
