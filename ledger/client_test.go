package ledger

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func event() Event {
	return Event{At: time.Now(), Actor: Actor{Kind: "system", ID: "s"},
		Action: Action{Verb: "job.run", Target: Target{System: "x", Kind: "job", ID: "1"}, Outcome: "ok"}}
}

func TestPost(t *testing.T) {
	var got Event
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer t" || r.URL.Path != "/v1/events" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewDecoder(r.Body).Decode(&got)
		if got.Action.Verb == "job.refused" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			io.WriteString(w, `{"status":"refused","reason":"bad_verb","error":"no"}`)
			return
		}
		w.WriteHeader(http.StatusCreated)
		io.WriteString(w, `{"status":"stored","id":"01JBX7Y8Z9ABCDEFGHJKMNPQRS"}`)
	}))
	defer srv.Close()
	c := New(srv.URL+"/", "t", "src")
	if err := c.Post(t.Context(), event()); err != nil {
		t.Fatal(err)
	}
	if got.Source != "src" {
		t.Fatalf("the source is the token's: %q", got.Source)
	}
	ev := event()
	ev.Action.Verb = "job.refused"
	var inv *ErrInvalid
	if err := c.Post(t.Context(), ev); !errors.As(err, &inv) || inv.Reason != "bad_verb" {
		t.Fatalf("refusal: %v", err)
	}
	ev = event()
	ev.Action.Outcome = "failed"
	if err := c.Post(t.Context(), ev); Reason(err) != "missing_reason" {
		t.Fatalf("an invalid event is refused before sending: %v", err)
	}
}

func TestNilClientPostsNothing(t *testing.T) {
	var c *Client = New("", "t", "s")
	if c != nil || c.Post(t.Context(), event()) != nil {
		t.Fatal("a client without a ledger must be a quiet nil")
	}
}

func TestClip(t *testing.T) {
	if got := Clip("a\nb\r\nc"); got != "a b c" {
		t.Fatalf("%q", got)
	}
	long := strings.Repeat("é", MaxTitle)
	got := Clip(long)
	if len(got) > MaxTitle || !utf8.ValidString(got) {
		t.Fatalf("%d bytes, valid %v", len(got), utf8.ValidString(got))
	}
}
