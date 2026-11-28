package annals

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/orchard-suite/platformkit/v3/change"
)

// record maps a change.stated event onto an Annals event, as Annals'
// docs/event.md says a sender does.
func record(e *change.Stated) Event {
	s := e.Statement
	ev := Event{
		At: e.At, Source: e.Sender, EventKey: e.Key,
		Actor:     Actor{Kind: s.Actor.Kind, ID: s.Actor.ID, OnBehalfOf: s.Actor.OnBehalfOf},
		Action:    Action{Verb: change.EventStated, Target: Target{System: "orrery", Kind: "change", ID: e.Key}, Outcome: "ok"},
		Change:    &Change{AfterHash: e.Digest},
		Statement: &Statement{Key: s.Key, Writer: s.Writer, Artifact: s.Artifact, Unknown: s.Unknown},
		Labels:    map[string]string{"applier": s.Applier, "writer": s.Writer.Name},
	}
	for _, t := range s.Things {
		ev.Items = append(ev.Items, Item{System: "catalog", Kind: t.Blueprint, ID: t.Key})
	}
	for _, ef := range s.Effects {
		ev.Effects = append(ev.Effects, Effect{
			Kind: ef.Kind, Subject: Subject{Blueprint: ef.Subject.Blueprint, Key: ef.Subject.Key},
			Path: ef.Path, Size: ef.Size, Undo: ef.Undo,
			Claim: ef.Claim.From, ClaimRule: ef.Claim.Rule, ClaimVersion: ef.Claim.Version,
		})
	}
	return ev
}

func workedStated(t *testing.T) *change.Stated {
	t.Helper()
	b, err := os.ReadFile("../change/schema/examples/event-stated.json")
	if err != nil {
		t.Fatal(err)
	}
	var e change.Stated
	if err := json.Unmarshal(b, &e); err != nil {
		t.Fatal(err)
	}
	if err := e.Validate(nil); err != nil {
		t.Fatalf("the worked example is not a valid change.stated event: %v", err)
	}
	if len(e.Statement.Unknown) == 0 || e.Statement.Effects[0].Claim.Rule == "" {
		t.Fatal("the worked example must carry an unknown part and a claim rule")
	}
	return &e
}

// A change.stated record keeps the whole statement: read back strictly, as
// Annals reads a post, it rebuilds the statement its digest names.
func TestStatedRecordKeepsTheWholeStatement(t *testing.T) {
	e := workedStated(t)
	b, err := json.Marshal(record(e))
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var ev Event
	if err := dec.Decode(&ev); err != nil {
		t.Fatalf("a strict read refuses the record: %v", err)
	}
	if err := ev.Validate(e.Sender); err != nil {
		t.Fatalf("the record is refused: %v", err)
	}
	got := ev.StatementOf()
	if !reflect.DeepEqual(got, e.Statement) {
		t.Fatalf("rebuilt statement differs:\n got %+v\nwant %+v", got, e.Statement)
	}
	if d, err := got.Digest(); err != nil || d != ev.Change.AfterHash {
		t.Errorf("rebuilt digest %s (%v), the record's %s", d, err, ev.Change.AfterHash)
	}
}

// A record stored before events had a statement part still reads, with
// its writer's name from its label and nothing else of the statement.
func TestStatedRecordStoredBefore(t *testing.T) {
	old := `{"at":"2026-11-26T15:00:00Z","source":"orrery","event_key":"run-1-stated",
		"actor":{"kind":"agent","id":"acme-agent"},
		"action":{"verb":"change.stated","target":{"system":"orrery","kind":"change","id":"run-1-stated"},"outcome":"ok"},
		"effects":[{"kind":"stops","subject":{"blueprint":"workload","key":"acme-web"},"path":"in-place","undo":"yes","claim":"estate"}],
		"labels":{"applier":"hub","writer":"infra"}}`
	dec := json.NewDecoder(strings.NewReader(old))
	dec.DisallowUnknownFields()
	var ev Event
	if err := dec.Decode(&ev); err != nil {
		t.Fatal(err)
	}
	if err := ev.Validate("orrery"); err != nil {
		t.Fatal(err)
	}
	st := ev.StatementOf()
	if st.Writer != (change.Writer{Name: "infra"}) || st.Applier != "hub" || st.Key != "" || st.Artifact != (change.Artifact{}) || st.Unknown != nil {
		t.Errorf("an older record rebuilds %+v", st)
	}
	if st.Effects[0].Claim != (change.Claim{From: "estate"}) {
		t.Errorf("an older effect's claim is %+v", st.Effects[0].Claim)
	}
}

// The statement part is held to the change package's rules, so it names
// parts and never holds a value, a secret, or an email.
func TestStatementRefusals(t *testing.T) {
	cases := []struct {
		reason string
		mutate func(*Event)
	}{
		{"missing_statement_key", func(ev *Event) { ev.Statement.Key = "" }},
		{"bad_slug", func(ev *Event) { ev.Statement.Writer.Name = "Infra" }},
		{"missing_statement_writer_version", func(ev *Event) { ev.Statement.Writer.Version = "" }},
		{"missing_statement_artifact_kind", func(ev *Event) { ev.Statement.Artifact.Kind = "" }},
		{"bad_digest", func(ev *Event) { ev.Statement.Artifact.Digest = "md5:abc" }},
		{"missing_part", func(ev *Event) { ev.Statement.Unknown[0].Part = "" }},
		{"bad_why", func(ev *Event) { ev.Statement.Unknown[0].Why = "skipped" }},
		{"bad_slug", func(ev *Event) { ev.Statement.Unknown[0].Thing.Blueprint = "Workload" }},
		{"value_in_name", func(ev *Event) { ev.Statement.Unknown[0].Part = "site_config.cors=*" }},
		{"secret_in_name", func(ev *Event) { ev.Statement.Unknown[0].Part = "xK9mQ2pL7vR4tY8wZ3nB6cF1hJ5dG0sA" }},
		{"email_in_name", func(ev *Event) { ev.Statement.Key = "plan-for-user@example.com" }},
		{"too_many_unknown", func(ev *Event) {
			ev.Statement.Unknown = make([]change.Unknown, MaxUnknown+1)
			for i := range ev.Statement.Unknown {
				ev.Statement.Unknown[i] = change.Unknown{Part: "tags", Why: change.WhyUnread}
			}
		}},
		{"email_in_name", func(ev *Event) { ev.Effects[0].ClaimRule = "ask user@example.com" }},
		{"value_in_name", func(ev *Event) { ev.Effects[0].ClaimVersion = "azurerm=4.20" }},
		{"missing_effects_claim", func(ev *Event) { ev.Effects[0].Claim = "" }},
	}
	for _, tc := range cases {
		ev := record(workedStated(t))
		tc.mutate(&ev)
		if err := ev.Validate(ev.Source); Reason(err) != tc.reason {
			t.Errorf("want %s, got %q (%v)", tc.reason, Reason(err), err)
		}
	}
}
