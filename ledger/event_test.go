package ledger

import (
	"testing"
	"time"
)

// Rules the schema cannot state are the validator's alone. The ledger's own
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
		t.Error("the ledger assigns ids")
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

// People belong in person fields, where the ledger can hide them from those
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
