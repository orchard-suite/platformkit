package orrery

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseRef(t *testing.T) {
	ref, err := ParseRef("repository:shop:api")
	if err != nil || ref.Blueprint != "repository" || ref.Key != "shop:api" || ref.ID() != "repository:shop:api" {
		t.Fatalf("%+v %v", ref, err)
	}
	for _, bad := range []string{"", "repository", ":key", "repository:"} {
		if _, err := ParseRef(bad); err == nil {
			t.Fatalf("%q should not parse", bad)
		}
	}
}

func TestRunActionForAPerson(t *testing.T) {
	var sent map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /v1/actions/lane_developer/run":
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &sent)
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":"r1","action":"lane_developer","entity":{"blueprint":"repository","key":"shop-api"},"by":"user@example.com","state":"awaiting","summary":null,"decidedBy":null,"tiers":["read"],"revises":"r0","note":null}`))
		case "GET /v1/action-runs/r1":
			_, _ = w.Write([]byte(`{"id":"r1","state":"returned","decidedBy":"lead@example.com","note":"name the table","tiers":[]}`))
		case "POST /v1/action-runs/r1/withdraw":
			_, _ = w.Write([]byte(`{}`))
		default:
			http.Error(w, "no", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "t", WithRetries(0))
	run, err := c.RunAction(t.Context(), "lane_developer", RunRequest{Entity: &EntityRef{"repository", "shop-api"}, Tiers: []string{TierRead}, Revises: "r0", For: "user@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	if run.ID != "r1" || run.State != RunAwaiting || run.Finished() || run.Summary != "" || run.Revises != "r0" || run.Entity.Key != "shop-api" {
		t.Fatalf("%+v", run)
	}
	if sent["for"] != "user@example.com" || sent["revises"] != "r0" || sent["inputs"] == nil {
		t.Fatalf("%v", sent)
	}
	got, err := c.ActionRun(t.Context(), "r1")
	if err != nil || !got.Finished() || got.State != RunReturned || got.Note != "name the table" || got.DecidedBy != "lead@example.com" {
		t.Fatalf("%+v %v", got, err)
	}
	if err := c.WithdrawRun(t.Context(), "r1", "replaced"); err != nil {
		t.Fatal(err)
	}
}

func TestTargetsFor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/actions/lane_user/targets" || r.URL.Query().Get("for") != "user@example.com" {
			http.Error(w, "no", http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"entities":[{"blueprint":"repository","key":"shop-api","title":"Shop API"},{"blueprint":"repository","key":"shop-web","title":"Shop web"}],"approval":["repository:shop-web"]}`))
	}))
	defer srv.Close()
	got, err := New(srv.URL, "t", WithRetries(0)).TargetsFor(t.Context(), "lane_user", "user@example.com")
	if err != nil || len(got) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	if got[0].Waits || !got[1].Waits || got[1].ID() != "repository:shop-web" || got[0].Title != "Shop API" {
		t.Fatalf("%+v", got)
	}
}

func TestToolsAndCallAreLentAndSentOnce(t *testing.T) {
	calls := 0
	var sent map[string]any
	var listed string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /v1/tools":
			listed = r.URL.RawQuery
			_, _ = w.Write([]byte(`{"tools":[{"id":"queue_depth","title":"Queue depth","tier":"read","inputs":{}}]}`))
		case "POST /v1/tools/queue_depth/call":
			calls++
			raw, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(raw, &sent)
			if calls == 1 {
				http.Error(w, "boom", http.StatusBadGateway)
				return
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"run":"t1","state":"succeeded","summary":"exit 0","text":"12\n","truncated":false,"outputs":{}}`))
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "t")
	repo := EntityRef{"repository", "shop-api"}
	lend := &Lend{Person: "user@example.com", Action: "lane_developer", Entity: repo, Run: "r1"}
	tools, err := c.Tools(t.Context(), repo, lend)
	if err != nil || len(tools) != 1 || tools[0].Tier != TierRead {
		t.Fatalf("%+v %v", tools, err)
	}
	if listed != "action=lane_developer&entity=repository%3Ashop-api&for=user%40example.com&run=r1" {
		t.Fatalf("query %s", listed)
	}
	// A failed call is not sent again: the tool may have run.
	_, err = c.CallTool(t.Context(), "queue_depth", ToolCall{Entity: &repo, As: lend})
	var apiErr *APIError
	if err == nil || calls != 1 || !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadGateway {
		t.Fatalf("calls %d err %v", calls, err)
	}
	answer, err := c.CallTool(t.Context(), "queue_depth", ToolCall{Entity: &repo, As: lend})
	if err != nil || answer.Run != "t1" || answer.Text != "12\n" || answer.State != RunSucceeded {
		t.Fatalf("%+v %v", answer, err)
	}
	as := sent["as"].(map[string]any)
	if as["person"] != "user@example.com" || as["run"] != "r1" || as["entity"].(map[string]any)["key"] != "shop-api" {
		t.Fatalf("%v", as)
	}
}
