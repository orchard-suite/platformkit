package dispatch

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/orchard-suite/platformkit/v3/change"
)

type seen struct {
	method, path, query, auth string
	body                      map[string]any
}

func desk(t *testing.T, answer func(w http.ResponseWriter, r *http.Request)) (*Client, *[]seen) {
	t.Helper()
	var calls []seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := seen{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery, auth: r.Header.Get("Authorization")}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &s.body)
		calls = append(calls, s)
		answer(w, r)
	}))
	t.Cleanup(srv.Close)
	return New(srv.URL+"/", "tok"), &calls
}

func TestNilClientDoesNothing(t *testing.T) {
	var c *Client
	if New("", "t") != nil || New("https://desk.example.org", "") != nil {
		t.Fatal("a client with no address or no token should be nil")
	}
	if err := c.Send(t.Context(), Event{Kind: "request.answered"}); err != nil {
		t.Fatal(err)
	}
	if err := c.DeclareKinds(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if got, err := c.OpenIncidents(t.Context(), "shop"); err != nil || got != nil {
		t.Fatalf("%v %v", got, err)
	}
	if got, err := c.OpenIncident(t.Context(), NewIncident{Title: "x"}); err != nil || got != nil {
		t.Fatalf("%v %v", got, err)
	}
	if err := c.RecordChange(t.Context(), Change{Service: "shop", Title: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Heartbeat(t.Context(), "shop", time.Hour); err != nil {
		t.Fatal(err)
	}
	if got, err := c.OnCall(t.Context()); err != nil || got != nil {
		t.Fatalf("%v %v", got, err)
	}
}

func TestSendAndDeclare(t *testing.T) {
	c, calls := desk(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) })
	ev := Event{Kind: "request.answered", Key: "req-1:answered", Title: "Your report was answered", Weight: Notify,
		Entity: &Ref{Blueprint: "repository", Key: "shop-api"}, To: []Target{{Person: "user@example.com"}}, Labels: map[string]string{"program": "shop"}}
	if err := c.Send(t.Context(), ev); err != nil {
		t.Fatal(err)
	}
	if err := c.DeclareKinds(t.Context(), []Kind{{Kind: "request.answered", Title: "A report was answered", Weight: Notify}}); err != nil {
		t.Fatal(err)
	}
	got := *calls
	if got[0].method != "POST" || got[0].path != "/v1/events" || got[0].auth != "Bearer tok" {
		t.Fatalf("%+v", got[0])
	}
	if got[0].body["weight"] != "notify" || got[0].body["to"].([]any)[0].(map[string]any)["person"] != "user@example.com" {
		t.Fatalf("%v", got[0].body)
	}
	if _, has := got[0].body["at"]; has {
		t.Fatal("an unset time should be left out")
	}
	if got[1].method != "PUT" || got[1].path != "/v1/kinds" {
		t.Fatalf("%+v", got[1])
	}
	kinds := got[1].body["kinds"].([]any)
	if len(kinds) != 1 || kinds[0].(map[string]any)["weight"] != "notify" {
		t.Fatalf("%v", kinds)
	}
}

func TestIncidents(t *testing.T) {
	c, calls := desk(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`[{"id":7,"title":"Shop is down","severity":"critical","state":"open","service":"shop","opened_at":"2026-01-02T03:04:05Z"}]`))
			return
		}
		w.Header().Set("Location", "https://desk.example.org/incidents/8")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":8,"title":"Orders stuck","severity":"warning","state":"open","service":"shop","opened_at":"2026-01-02T03:04:05Z"}`))
	})
	open, err := c.OpenIncidents(t.Context(), "shop")
	if err != nil || len(open) != 1 || open[0].ID != 7 || open[0].Severity != Critical || open[0].OpenedAt.Year() != 2026 {
		t.Fatalf("%+v %v", open, err)
	}
	if q := (*calls)[0].query; q != "service=shop&state=open%2Cacknowledged" {
		t.Fatalf("query %s", q)
	}
	made, err := c.OpenIncident(t.Context(), NewIncident{Title: "Orders stuck", Service: "shop", Severity: Warning})
	if err != nil || made.ID != 8 || made.URL != "https://desk.example.org/incidents/8" {
		t.Fatalf("%+v %v", made, err)
	}
}

func TestChangeHeartbeatOnCall(t *testing.T) {
	c, calls := desk(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/schedules" {
			_, _ = w.Write([]byte(`[{"slug":"platform","name":"Platform","person":"","until":"2026-01-02T03:04:05Z"}]`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})
	if err := c.RecordChange(t.Context(), Change{Service: "shop", Title: "Release 1.2.3", URL: "https://forge.example.org/shop/commit/abc",
		Things: []change.Ref{{Blueprint: "sw_service", Key: "shop"}}, Run: "https://forge.example.org/shop/runs/7", Record: &change.EventRef{Sender: "forge", Key: "run-7"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Heartbeat(t.Context(), "slipway-shop", 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	on, err := c.OnCall(t.Context())
	if err != nil || len(on) != 1 || on[0].Slug != "platform" || on[0].Person != "" {
		t.Fatalf("%+v %v", on, err)
	}
	got := *calls
	things, _ := got[0].body["things"].([]any)
	record, _ := got[0].body["record"].(map[string]any)
	if got[0].path != "/v1/changes" || got[0].body["service"] != "shop" || len(things) != 1 || got[0].body["run"] != "https://forge.example.org/shop/runs/7" || record["key"] != "run-7" {
		t.Fatalf("%+v", got[0])
	}
	if got[1].path != "/v1/heartbeats/slipway-shop" || got[1].query != "interval=5m0s" {
		t.Fatalf("%+v", got[1])
	}
}

func TestRefusalIsAnError(t *testing.T) {
	c, _ := desk(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"no group called nobody"}`))
	})
	err := c.Send(t.Context(), Event{Kind: "request.answered", To: []Target{{Group: "nobody"}}})
	var refused *Error
	if !errors.As(err, &refused) || refused.Status != 400 || refused.Message != "no group called nobody" {
		t.Fatalf("%v", err)
	}
}
