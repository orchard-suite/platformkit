package annals

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProofReadsOneKey(t *testing.T) {
	var asked string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer t" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		asked = r.URL.RequestURI()
		io.WriteString(w, `{"kind":"stops","blueprint":"app","path":"swap","watched":4,"clean":3,"harm":0,"blind":1,"last_seen":"2026-11-20T10:00:00Z"}`)
	}))
	defer srv.Close()
	p, err := New(srv.URL, "t", "trellis").Proof(t.Context(), ProofKey{Kind: "stops", Blueprint: "app", Path: "swap"})
	if err != nil {
		t.Fatal(err)
	}
	if asked != "/v1/proof?blueprint=app&kind=stops&path=swap" {
		t.Fatalf("asked %s", asked)
	}
	if p.Watched != 4 || p.Clean != 3 || p.Blind != 1 || p.LastSeen == nil {
		t.Fatalf("read %+v", p)
	}
}

func TestReadsSayWhyTheyFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"error":"kind is required, a lowercase slug","reason":"bad_filter"}`)
	}))
	defer srv.Close()
	if _, err := New(srv.URL, "t", "trellis").Proof(t.Context(), ProofKey{}); err == nil {
		t.Fatal("a refused read is an error")
	}
	var c *Client
	if _, err := c.Proof(t.Context(), ProofKey{}); !errors.Is(err, ErrNoClient) {
		t.Fatalf("a nil client has no Annals to read: %v", err)
	}
	if _, err := c.Events(t.Context(), Query{}); !errors.Is(err, ErrNoClient) {
		t.Fatalf("a nil client has no Annals to read: %v", err)
	}
}

func TestEachEventReadsEveryPage(t *testing.T) {
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		if r.URL.Query().Get("cursor") == "" {
			io.WriteString(w, `{"events":[{"id":"a","source":"orrery","event_key":"r1.stated","at":"2026-11-20T10:00:00Z","actor":{"kind":"person","id":"p-1"},"action":{"verb":"change.stated","target":{"system":"orrery","kind":"change","id":"r1.stated"},"outcome":"ok"}}],"next":"c2"}`)
			return
		}
		io.WriteString(w, `{"events":[{"id":"b","source":"orrery","event_key":"r0.stated","at":"2026-11-19T10:00:00Z","actor":{"kind":"person","id":"p-1"},"action":{"verb":"change.stated","target":{"system":"orrery","kind":"change","id":"r0.stated"},"outcome":"ok"}}]}`)
	}))
	defer srv.Close()
	since := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	var keys []string
	err := New(srv.URL, "t", "trellis").EachEvent(t.Context(), Query{Since: since, Verb: "change.stated", Limit: 500}, func(ev Event) error {
		keys = append(keys, ev.EventKey)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0] != "r1.stated" || keys[1] != "r0.stated" {
		t.Fatalf("read %v", keys)
	}
	if queries[0] != "limit=500&since=2026-11-01T00%3A00%3A00Z&verb=change.stated" || queries[1] != "cursor=c2&limit=500&since=2026-11-01T00%3A00%3A00Z&verb=change.stated" {
		t.Fatalf("asked %v", queries)
	}
}
