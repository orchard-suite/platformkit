package orrery

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

const descriptorYAML = `
service: github/acme/tally/cmd/tally
fields: { category: Cost }
relations: { pillars: [finance/cost] }
provides:
  - id: costs
    title: Tally costs API
    kind: rest
    description: What things cost.
consumes: [github/acme/orrery/catalog]
`

func TestParseDescriptorRefusesWhatTheHubWould(t *testing.T) {
	cases := map[string]string{
		"no service":   "category: Cost\n",
		"unknown key":  "service: s\nowner: someone\n",
		"bad kind":     "service: s\nprovides: [{id: a, title: A, kind: soap}]\n",
		"no api id":    "service: s\nprovides: [{title: A, kind: rest}]\n",
		"api repeated": "service: s\nprovides: [{id: a, title: A, kind: rest}, {id: a, title: B, kind: rest}]\n",
	}
	for name, doc := range cases {
		if _, err := ParseDescriptor([]byte(doc)); err == nil {
			t.Errorf("%s: parsed, want an error", name)
		}
	}
	if _, err := ParseDescriptor([]byte(descriptorYAML)); err != nil {
		t.Fatal(err)
	}
}

type write struct {
	method string
	path   string
	body   map[string]any
}

func TestDescribePatchesTheServiceAndPutsItsAPIs(t *testing.T) {
	var mu sync.Mutex
	var writes []write
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		writes = append(writes, write{r.Method, r.URL.Path, body})
		mu.Unlock()
		if r.URL.Path == "/ingest/blueprints" {
			json.NewEncoder(w).Encode(map[string]any{"created": []string{}, "extended": []string{"service"}, "kept": []string{"api"}})
			return
		}
		w.WriteHeader(200)
		w.Write([]byte(`{"outcome":"updated"}`))
	}))
	defer srv.Close()
	d, err := ParseDescriptor([]byte(descriptorYAML))
	if err != nil {
		t.Fatal(err)
	}
	if err := New(srv.URL, "tok", WithRetries(0)).Describe(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if len(writes) != 3 {
		t.Fatalf("got %d writes, want offer, service, api", len(writes))
	}
	if writes[0].method != "PUT" || writes[0].path != "/ingest/blueprints" {
		t.Errorf("first write %s %s, want the blueprint offer", writes[0].method, writes[0].path)
	}
	svc := writes[1]
	if svc.method != "PATCH" || svc.path != "/ingest/entities/sw_service/github/acme/tally/cmd/tally" {
		t.Errorf("service written with %s %s, want a PATCH so other sources keep their fields", svc.method, svc.path)
	}
	rel := svc.body["relations"].(map[string]any)
	if got := rel["pillars"]; got == nil || len(got.([]any)) != 1 {
		t.Errorf("pillars = %v, want the file's placement written through", got)
	}
	if got := rel["will_consume"]; got == nil || len(got.([]any)) != 0 {
		t.Errorf("will_consume = %v, want an empty list that clears it", got)
	}
	if strings.Contains(svc.path, "runs") || svc.body["fields"].(map[string]any)["origin"] != "built" {
		t.Errorf("service body %v", svc.body)
	}
	api := writes[2]
	if api.method != "PUT" || api.path != "/ingest/entities/sw_api/github/acme/tally/cmd/tally/costs" {
		t.Errorf("api written with %s %s", api.method, api.path)
	}
	if api.body["relations"].(map[string]any)["service"] != "github/acme/tally/cmd/tally" {
		t.Errorf("api not tied to its service: %v", api.body)
	}
}

func TestDescribeStopsAtARefusedOffer(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		json.NewEncoder(w).Encode(map[string]any{"rejected": []map[string]string{{"blueprint": "service", "reason": "not registered"}}})
	}))
	defer srv.Close()
	d, _ := ParseDescriptor([]byte(descriptorYAML))
	if err := New(srv.URL, "tok", WithRetries(0)).Describe(context.Background(), d); err == nil {
		t.Fatal("described, want the refusal")
	}
	if calls != 1 {
		t.Errorf("%d calls, want only the offer", calls)
	}
}

func TestDescribeLeavesAnEstatesSchemeAlone(t *testing.T) {
	d, err := ParseDescriptor([]byte("service: github/acme/hub\nconsumes: [github/acme/ledger/events]\n"))
	if err != nil {
		t.Fatal(err)
	}
	svc := d.Entities()[0]
	if len(svc.Fields) != 1 || len(svc.Relations) != 2 {
		t.Errorf("placement written: %v %v", svc.Fields, svc.Relations)
	}
}
