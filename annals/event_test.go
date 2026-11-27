package annals

import (
	"testing"
	"time"
)

// Rules the schema cannot state are the validator's alone. Annals' own
// tests hold the validator to its JSON Schema on every worked example.
func TestValidatorOnlyRules(t *testing.T) {
	good := func() Event {
		return Event{At: time.Now(), Source: "ion", Actor: Actor{Kind: "agent", ID: "a"},
			Action: Action{Verb: "tool.bash", Target: Target{System: "workstation", Kind: "dir", ID: "/x"}, Outcome: "ok"}}
	}
	ev := good()
	ev.Source = "other"
	if Reason(ev.Validate("ion")) != "source_mismatch" {
		t.Error("a token for one source must not post as another")
	}
	ev = good()
	ev.Source = ""
	if err := ev.Validate("ion"); err != nil || ev.Source != "ion" {
		t.Errorf("an empty source takes the token's: %v %q", err, ev.Source)
	}
	ev = good()
	ev.Action.Outcome = "failed"
	if Reason(ev.Validate("ion")) != "missing_reason" {
		t.Error("a failed outcome needs a reason")
	}
	ev = good()
	ev.Actor = Actor{Kind: "person", ID: "p", OnBehalfOf: "q"}
	if Reason(ev.Validate("ion")) != "bad_on_behalf_of" {
		t.Error("a person acts for nobody")
	}
	ev = good()
	ev.Authority = &Authority{Approval: &Approval{Required: true, At: time.Now()}}
	if Reason(ev.Validate("ion")) != "bad_approval" {
		t.Error("an approval with a time names who gave it")
	}
	ev = good()
	ev.ID = "01JBX7Y8Z9ABCDEFGHJKMNPQRS"
	if Reason(ev.Validate("ion")) != "id_set" {
		t.Error("Annals assigns ids")
	}
	ev = good()
	ev.Corrects = "not-a-ulid"
	if Reason(ev.Validate("ion")) != "bad_corrects" {
		t.Error("corrects must be a ULID")
	}
	ev = good()
	ev.Cause = "k1"
	ev.EventKey = "k1"
	if Reason(ev.Validate("ion")) != "bad_cause" {
		t.Error("an event cannot cause itself")
	}
}

// People belong in person fields, where Annals can hide them from those
// not allowed to see people. Free text never holds an email address.
func TestPeopleOnlyInPersonFields(t *testing.T) {
	good := func() Event {
		return Event{At: time.Now(), Source: "m365", Actor: Actor{Kind: "person", ID: "u1", Email: "owner@example.com"},
			Action:  Action{Verb: "site.share", Target: Target{System: "sharepoint", Kind: "site", ID: "s1", Title: "Finance"}, Outcome: "ok"},
			Parties: []Party{{Role: "recipient", Kind: "person", ID: "g1", Email: "guest@example.org", External: true}}}
	}
	ev := good()
	if err := ev.Validate("m365"); err != nil {
		t.Fatalf("a share with a guest is a good event: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*Event)
		reason string
	}{
		{"email in a title", func(e *Event) { e.Action.Target.Title = "shared with guest@example.org" }, "email_in_text"},
		{"email in a summary", func(e *Event) { e.Change = &Change{Summary: "added owner@example.com"} }, "email_in_text"},
		{"email in a label", func(e *Event) { e.Labels = map[string]string{"by": "owner@example.com"} }, "email_in_text"},
		{"email in an item title", func(e *Event) {
			e.Items = []Item{{System: "sharepoint", Kind: "file", ID: "f", Title: "owner@example.com notes"}}
		}, "email_in_text"},
		{"actor email not an email", func(e *Event) { e.Actor.Email = "owner" }, "bad_email"},
		{"party email not an email", func(e *Event) { e.Parties[0].Email = "guest" }, "bad_email"},
		{"party kind unknown", func(e *Event) { e.Parties[0].Kind = "robot" }, "bad_party_kind"},
		{"party with no id", func(e *Event) { e.Parties[0].ID = "" }, "missing_parties_id"},
		{"party role not a slug", func(e *Event) { e.Parties[0].Role = "Recipient" }, "bad_slug"},
		{"item with no id", func(e *Event) { e.Items = []Item{{System: "sharepoint", Kind: "file"}} }, "missing_items_id"},
		{"too many parties", func(e *Event) { e.Parties = make([]Party, MaxParties+1) }, "too_many_parties"},
		{"too many items", func(e *Event) { e.Items = make([]Item, MaxItems+1) }, "too_many_items"},
	}
	for _, tc := range cases {
		ev := good()
		tc.mutate(&ev)
		if got := Reason(ev.Validate("m365")); got != tc.reason {
			t.Errorf("%s: reason %q, want %q", tc.name, got, tc.reason)
		}
	}
	ev = good()
	ev.Parties = []Party{{Role: "recipient", Kind: "anyone"}}
	if err := ev.Validate("m365"); err != nil {
		t.Errorf("an anyone link names nobody: %v", err)
	}
}

// A cause names an event by its source and key, so a record can be set off
// by another sender's event.
func TestCauseAcrossSources(t *testing.T) {
	good := func() Event {
		return Event{At: time.Now(), Source: "dispatch", EventKey: "k1", Actor: Actor{Kind: "system", ID: "watch"},
			Action: Action{Verb: "change.observed", Target: Target{System: "orrery", Kind: "change", ID: "c1"}, Outcome: "ok"}}
	}
	ev := good()
	ev.Cause, ev.CauseSource = "k1", "orrery"
	if err := ev.Validate("dispatch"); err != nil {
		t.Errorf("the same key from another source is another event: %v", err)
	}
	if ev.CauseFrom() != "orrery" {
		t.Errorf("CauseFrom = %q, want orrery", ev.CauseFrom())
	}
	ev = good()
	ev.Cause, ev.CauseSource = "k1", "dispatch"
	if Reason(ev.Validate("dispatch")) != "bad_cause" {
		t.Error("an event cannot cause itself, its source named or not")
	}
	ev = good()
	ev.Cause = "k0"
	if ev.CauseFrom() != "dispatch" {
		t.Errorf("an empty cause_source is the event's own source, got %q", ev.CauseFrom())
	}
	ev = good()
	ev.CauseSource = "orrery"
	if Reason(ev.Validate("dispatch")) != "bad_cause_source" {
		t.Error("cause_source needs a cause")
	}
	ev = good()
	ev.Cause, ev.CauseSource = "k0", "Orrery"
	if Reason(ev.Validate("dispatch")) != "bad_slug" {
		t.Error("cause_source is a source, a lowercase slug")
	}
}

// Effects are names and kinds only: what a change did to which thing, by
// which path, never a value.
func TestEffects(t *testing.T) {
	size := int64(3)
	good := func() Event {
		return Event{At: time.Now(), Source: "orrery", EventKey: "s1", Actor: Actor{Kind: "agent", ID: "a"},
			Action: Action{Verb: "change.stated", Target: Target{System: "orrery", Kind: "change", ID: "s1"}, Outcome: "ok"},
			Effects: []Effect{
				{Kind: "stops", Subject: Subject{Blueprint: "workload", Key: "acme-web"}, Path: "in-place", Undo: "yes", Claim: "estate"},
				{Kind: "costs", Subject: Subject{Blueprint: "plan", Key: "acme-plan"}, Path: "in-place", Size: &size, Undo: "partial", Claim: "writer"},
				{Kind: "stops", Subject: Subject{Blueprint: "workload", Key: "acme-api"}, Path: "swap", Verdict: "blind"},
			}}
	}
	ev := good()
	if err := ev.Validate("orrery"); err != nil {
		t.Fatalf("stated and observed effects are good: %v", err)
	}
	neg := int64(-1)
	cases := []struct {
		name   string
		mutate func(*Effect)
		reason string
	}{
		{"no kind", func(e *Effect) { e.Kind = "" }, "missing_effects_kind"},
		{"kind not a slug", func(e *Effect) { e.Kind = "Stops" }, "bad_slug"},
		{"no blueprint", func(e *Effect) { e.Subject.Blueprint = "" }, "missing_effects_subject_blueprint"},
		{"no key", func(e *Effect) { e.Subject.Key = "" }, "missing_effects_subject_key"},
		{"email as a key", func(e *Effect) { e.Subject.Key = "owner@example.com" }, "email_in_text"},
		{"key with a newline", func(e *Effect) { e.Subject.Key = "a\nb" }, "bad_text"},
		{"no path", func(e *Effect) { e.Path = "" }, "missing_effects_path"},
		{"path not a slug", func(e *Effect) { e.Path = "In Place" }, "bad_slug"},
		{"negative size", func(e *Effect) { e.Size = &neg }, "bad_size"},
		{"unknown undo", func(e *Effect) { e.Undo = "maybe" }, "bad_undo"},
		{"unknown claim", func(e *Effect) { e.Claim = "guess" }, "bad_claim"},
		{"unknown verdict", func(e *Effect) { e.Verdict = "fine" }, "bad_verdict"},
	}
	for _, tc := range cases {
		ev := good()
		tc.mutate(&ev.Effects[0])
		if got := Reason(ev.Validate("orrery")); got != tc.reason {
			t.Errorf("%s: reason %q, want %q", tc.name, got, tc.reason)
		}
	}
	ev = good()
	ev.Effects = make([]Effect, MaxEffects+1)
	if Reason(ev.Validate("orrery")) != "too_many_effects" {
		t.Error("at most MaxEffects effects")
	}
}
