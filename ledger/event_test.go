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
}
