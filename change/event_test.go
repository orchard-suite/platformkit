package change

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func head(of bool) Header {
	h := Header{Sender: "orrery", Key: "e1", At: time.Now(), Digest: "sha256:" + strings.Repeat("a", 64)}
	if of {
		h.Of = &EventRef{Sender: "orrery", Key: "s1"}
	}
	return h
}

// A stated event carries its statement's own digest, and starts the change.
func TestStatedDigestAndOf(t *testing.T) {
	s := good()
	d, _ := s.Digest()
	e := &Stated{Header: head(false), Statement: s}
	e.Digest = d
	if err := e.Validate(nil); err != nil {
		t.Fatalf("a good stated event is refused: %v", err)
	}
	if e.Event != EventStated {
		t.Errorf("validate fills the event name: %q", e.Event)
	}
	e.Digest = "sha256:" + strings.Repeat("b", 64)
	if Reason(e.Validate(nil)) != "digest_mismatch" {
		t.Error("a stated event's digest is its statement's")
	}
	e.Digest = d
	e.Of = &EventRef{Sender: "orrery", Key: "x"}
	if Reason(e.Validate(nil)) != "bad_of" {
		t.Error("a stated event names no other")
	}
}

// Every later event names the change it belongs to.
func TestLaterEventsNameTheChange(t *testing.T) {
	e := &Decided{Header: head(false), By: Actor{Kind: ActorPerson, ID: "user@example.com"}, Answer: AnswerYes}
	if Reason(e.Validate(nil)) != "missing_of" {
		t.Error("a decided event names its change")
	}
	e.Header = head(true)
	if err := e.Validate(nil); err != nil {
		t.Errorf("a good decided event is refused: %v", err)
	}
	e.Event = EventEnded
	if Reason(e.Validate(nil)) != "event_mismatch" {
		t.Error("an event's name is its type's")
	}
}

// The fences hold in the event itself: a fence never routes to no one, and
// a route with no judge is to people by the judge_down fence.
func TestFences(t *testing.T) {
	zero := 0
	people := Route{To: RoutePeople, Grants: []Grant{{Values: []string{"admins"}}}, Quorum: 1}
	judge := &Judge{Name: "trellis", Version: "1", Pack: "gate"}
	cases := []struct {
		reason string
		e      Routed
	}{
		{"fence_ignored", Routed{Header: head(true), Judge: judge, Route: Route{To: RouteNoOne}, Reasons: []RouteReason{{Part: &zero, Fence: FenceUnknown}}}},
		{"missing_judge", Routed{Header: head(true), Route: Route{To: RouteNoOne}, Reasons: []RouteReason{{Effect: &zero}}}},
		{"missing_judge", Routed{Header: head(true), Route: people, Reasons: []RouteReason{{Effect: &zero}}}},
		{"bad_quorum", Routed{Header: head(true), Judge: judge, Route: Route{To: RoutePeople, Grants: people.Grants}}},
		{"empty_reason", Routed{Header: head(true), Judge: judge, Route: people, Reasons: []RouteReason{{Rule: "r"}}}},
		{"email_in_name", Routed{Header: head(true), Judge: judge, Route: people, Reasons: []RouteReason{{Effect: &zero, Rule: "ask user@example.com"}}}},
	}
	for _, c := range cases {
		if got := Reason(c.e.Validate(nil)); got != c.reason {
			t.Errorf("want %s, got %q", c.reason, got)
		}
	}
	down := Routed{Header: head(true), Route: people, Reasons: []RouteReason{{Fence: FenceJudgeDown}}}
	if err := down.Validate(nil); err != nil {
		t.Errorf("a judge-down route to people is refused: %v", err)
	}
}

// A statement that differed is refused, and says how.
func TestEndedCheck(t *testing.T) {
	e := &Ended{Header: head(true), Outcome: OutcomeRefused, Check: CheckDiffered}
	if Reason(e.Validate(nil)) != "missing_differs" {
		t.Error("a statement that differed names what differed")
	}
	e.Differs = []string{"summary"}
	if Reason(e.Validate(nil)) != "bad_differs" {
		t.Error("differs names statement fields only")
	}
	e.Differs = []string{"effects"}
	e.Outcome = OutcomeOK
	if Reason(e.Validate(nil)) != "ran_differed" {
		t.Error("a statement that differed never runs")
	}
}

// Blind is its own verdict, and the kinds it names are registered.
func TestObserved(t *testing.T) {
	now := time.Now()
	e := &Observed{Header: head(true), Watch: Watcher{Name: "dispatch", Version: "1"}, From: now, To: now.Add(time.Minute),
		Sights: []Sight{{Kind: "stops", Subject: Ref{Blueprint: "workload", Key: "a"}, Verdict: VerdictBlind}}}
	if err := e.Validate(nil); err != nil {
		t.Fatalf("a good observation is refused: %v", err)
	}
	e.Sights[0].Kind = "halts"
	if Reason(e.Validate(nil)) != "unknown_kind" {
		t.Error("a sight's kind is registered")
	}
	e.Sights[0].Kind = "stops"
	e.To = now.Add(-time.Minute)
	if Reason(e.Validate(nil)) != "bad_window" {
		t.Error("a window never ends before it starts")
	}
}

// Decode gives back the event's own type, and refuses a field the
// contract does not have.
func TestDecode(t *testing.T) {
	e := &Decided{Header: head(true), By: Actor{Kind: ActorAgent, ID: "a"}, Answer: AnswerNo}
	if err := e.Validate(nil); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(e)
	got, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	if d, ok := got.(*Decided); !ok || d.Answer != AnswerNo || d.Head().Of.Key != "s1" {
		t.Errorf("decode: %#v", got)
	}
	bad := strings.Replace(string(b), `"answer"`, `"note":"x","answer"`, 1)
	if _, err := Decode([]byte(bad)); err == nil {
		t.Error("an extra field is refused")
	}
	if _, err := Decode([]byte(`{"event":"change.guessed"}`)); Reason(err) != "unknown_event" {
		t.Errorf("an unknown event: %v", err)
	}
}

// A base kind keeps its family and unit; an estate may change its watch.
func TestBaseKindsHold(t *testing.T) {
	k := Base()
	if err := k.Add(Kind{Name: "stops", Family: FamilyExposure, Means: "x", Unit: "copies"}); Reason(err) != "base_kind_changed" {
		t.Errorf("a base kind's family is the suite's: %v", err)
	}
	if err := k.Add(Kind{Name: "stops", Family: FamilyDisruption, Means: "x", Unit: "copies", Watch: Duration(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if got, _ := k.Get("stops"); got.Watch != Duration(time.Hour) || got.Means != "It stops or restarts." {
		t.Errorf("only the watch changes: %+v", got)
	}
	if n := len(Base().All()); n != 6 {
		t.Errorf("the base set is six kinds, got %d", n)
	}
	if got, _ := Base().Get("stops"); got.Watch != Duration(30*time.Minute) {
		t.Error("Base is a fresh registry each time")
	}
	if _, err := LoadKinds(strings.NewReader(`{"kinds":[{"name":"sends","family":"exposure","means":"x"},{"name":"sends","family":"exposure","means":"y"}]}`)); Reason(err) != "duplicate_kind" {
		t.Errorf("a file names a kind once: %v", err)
	}
}
