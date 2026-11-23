package annals

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
		t.Fatal("a client without Annals must be a quiet nil")
	}
}

func TestClip(t *testing.T) {
	if got := Clip("a\nb\r\nc"); got != "a b c" {
		t.Fatalf("%q", got)
	}
	ev := Event{Source: "dispatch", Actor: Actor{Kind: "system", ID: "dispatch"}, Action: Action{Verb: "notice.accept", Target: Target{System: "dispatch", Kind: "notice", ID: "7",
		Title: Clip("Pat@Example.com changed the blueprint")}, Outcome: "ok"}, At: time.Now()}
	if ev.Action.Target.Title != "[email] changed the blueprint" {
		t.Fatalf("an email in text is replaced: %q", ev.Action.Target.Title)
	}
	if err := ev.Validate("dispatch"); err != nil {
		t.Fatalf("a clipped title passes Annals: %v", err)
	}
	long := strings.Repeat("é", MaxTitle)
	got := Clip(long)
	if len(got) > MaxTitle || !utf8.ValidString(got) {
		t.Fatalf("%d bytes, valid %v", len(got), utf8.ValidString(got))
	}
}

func TestPostBatch(t *testing.T) {
	var posts [][]map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var batch []map[string]any
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			t.Errorf("not an array: %v", err)
		}
		posts = append(posts, batch)
		res := make([]Receipt, len(batch))
		for i := range batch {
			res[i] = Receipt{Index: i, Status: "stored", ID: "01JBX7Y8Z9ABCDEFGHJKMNPQRS"}
		}
		json.NewEncoder(w).Encode(map[string]any{"results": res})
	}))
	defer srv.Close()
	c := New(srv.URL, "lg_x", "s")
	evs := make([]Event, MaxBatch+2)
	for i := range evs {
		evs[i] = event()
	}
	evs[1].Action.Outcome = "maybe"
	got, err := c.PostBatch(t.Context(), evs)
	if err != nil {
		t.Fatal(err)
	}
	if len(posts) != 2 || len(posts[0]) != MaxBatch-1 || len(posts[1]) != 2 {
		t.Errorf("batches of at most %d, the refused one left out: %d posts", MaxBatch, len(posts))
	}
	if got[1].Status != "refused" || got[1].Reason != "bad_outcome" || got[0].Status != "stored" || got[MaxBatch+1].Status != "stored" || got[MaxBatch+1].Index != MaxBatch+1 {
		t.Errorf("receipts: %+v %+v %+v", got[0], got[1], got[MaxBatch+1])
	}
	var nilClient *Client
	if rs, err := nilClient.PostBatch(t.Context(), evs); err != nil || len(rs) != len(evs) {
		t.Error("a nil client posts nothing")
	}
}
