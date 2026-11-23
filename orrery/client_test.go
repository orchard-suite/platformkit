package orrery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestRunBatchesAndCommits(t *testing.T) {
	var batches, commits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.Method == "POST" && r.URL.Path == "/ingest/runs":
			json.NewEncoder(w).Encode(map[string]string{"id": "r1"})
		case r.Method == "PUT" && r.URL.Path == "/ingest/runs/r1/entities":
			var body struct {
				Entities []Entity `json:"entities"`
			}
			json.NewDecoder(r.Body).Decode(&body)
			batches.Add(1)
			if len(body.Entities) > 2 {
				t.Errorf("batch too large: %d", len(body.Entities))
			}
			json.NewEncoder(w).Encode(map[string]any{"accepted": len(body.Entities), "rejected": []WriteError{{Blueprint: "x", Key: "bad", Reason: "nope"}}})
		case r.Method == "POST" && r.URL.Path == "/ingest/runs/r1/commit":
			commits.Add(1)
			json.NewEncoder(w).Encode(RunResult{ID: "r1", State: "committed", Counts: RunCounts{Created: 5}})
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "tok", WithRetries(0))
	res, err := c.Snapshot(context.Background(), "home", func(add func(Entity) error) error {
		for i := 0; i < 5; i++ {
			if err := add(Entity{Blueprint: "x", Key: string(rune('a' + i))}); err != nil {
				return err
			}
		}
		return nil
	}, BatchSize(2))
	if err != nil {
		t.Fatal(err)
	}
	if res.Counts.Created != 5 || commits.Load() != 1 || batches.Load() != 3 {
		t.Fatalf("got created=%d commits=%d batches=%d", res.Counts.Created, commits.Load(), batches.Load())
	}
}

func TestSnapshotFailsRunOnError(t *testing.T) {
	var failed atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ingest/runs":
			json.NewEncoder(w).Encode(map[string]string{"id": "r2"})
		case "/ingest/runs/r2/fail":
			failed.Store(true)
			w.Write([]byte(`{}`))
		case "/ingest/runs/r2/commit":
			t.Error("commit must not be called")
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "tok", WithRetries(0))
	_, err := c.Snapshot(context.Background(), "home", func(add func(Entity) error) error {
		return context.DeadlineExceeded
	})
	if err == nil || !failed.Load() {
		t.Fatalf("expected failure and a fail call, got err=%v failed=%v", err, failed.Load())
	}
}

func TestRetriesOn5xxNotOn4xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n == 1 {
			w.WriteHeader(503)
			return
		}
		if r.URL.Path == "/ingest/entities/x/bad" {
			w.WriteHeader(422)
			w.Write([]byte(`{"error":"rejected","errors":[{"blueprint":"x","key":"bad","field":"f","reason":"expected a number"}]}`))
			return
		}
		w.Write([]byte(`{"outcome":"created"}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "tok", WithRetries(2))
	if err := c.Put(context.Background(), Entity{Blueprint: "x", Key: "ok"}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected one retry, got %d calls", calls.Load())
	}
	before := calls.Load()
	err := c.Put(context.Background(), Entity{Blueprint: "x", Key: "bad"})
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Status != 422 || len(apiErr.Errors) != 1 || apiErr.Errors[0].Field != "f" {
		t.Fatalf("expected a 422 with one validation error, got %v", err)
	}
	if calls.Load() != before+1 {
		t.Fatalf("4xx must not retry")
	}
}

func TestPutAction(t *testing.T) {
	var got map[string]any
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.Method + " " + r.URL.Path
		json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(200)
	}))
	defer srv.Close()
	c := New(srv.URL, "tok", WithRetries(0))
	if err := c.PutAction(context.Background(), "build app", Action{"title": "Build"}); err != nil {
		t.Fatal(err)
	}
	if path != "PUT /api/actions/build app" || got["title"] != "Build" {
		t.Fatalf("%s %v", path, got)
	}
}
