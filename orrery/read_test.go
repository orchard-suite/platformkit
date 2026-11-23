package orrery

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestEntitiesPagesAndFilters(t *testing.T) {
	var filters []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer t" || r.URL.Path != "/api/entities" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		filters = append(filters, r.URL.Query().Get("filter"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		n := min(PageSize, 2500-offset)
		entities := make([]map[string]any, n)
		for i := range n {
			entities[i] = map[string]any{"key": strconv.Itoa(offset + i), "fields": map[string]any{"n": offset + i, "on": true, "at": "2026-01-02T03:04:05Z"}, "relations": map[string]any{"workload": []map[string]string{{"key": "w"}}}}
		}
		json.NewEncoder(w).Encode(map[string]any{"total": 2500, "entities": entities})
	}))
	defer srv.Close()
	c := New(srv.URL, "t", WithRetries(0))
	got, err := c.Entities(t.Context(), Query{Blueprint: "k8s_namespace", Filters: []Filter{{"name", "eq", "a"}, {"tier", "eq", "b"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2500 || len(filters) != 2 {
		t.Fatalf("%d entities in %d pages", len(got), len(filters))
	}
	if !strings.HasPrefix(filters[0], `{"and":[`) {
		t.Fatalf("filter %s", filters[0])
	}
	r := got[2001]
	if n, ok := r.Number("n"); !ok || n != 2001 {
		t.Fatalf("number %v %v", n, ok)
	}
	if b, ok := r.Bool("on"); !ok || !b {
		t.Fatal("bool")
	}
	if _, ok := r.Bool("missing"); ok {
		t.Fatal("a missing field was set")
	}
	if at, ok := r.Time("at"); !ok || at.Year() != 2026 {
		t.Fatal("time")
	}
	if ref, ok := r.Related("workload"); !ok || ref.Key != "w" {
		t.Fatal("relation")
	}
}

func TestActionsFor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/actions" || r.URL.Query().Get("for") != "a+b@example.com" {
			http.Error(w, "no", http.StatusBadRequest)
			return
		}
		w.Write([]byte(`{"actions":[{"id":"deploy","title":"Deploy","group":"Ops","approval":true,"lastRun":null}]}`))
	}))
	defer srv.Close()
	got, err := New(srv.URL, "t", WithRetries(0)).ActionsFor(t.Context(), "a+b@example.com")
	if err != nil || len(got) != 1 || got[0].ID != "deploy" || !got[0].Approval || got[0].Group != "Ops" {
		t.Fatalf("%+v %v", got, err)
	}
}
