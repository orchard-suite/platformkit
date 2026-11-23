package orrery

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
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
    spec: api/openapi.yaml
consumes: [github/acme/orrery/catalog]
`

func TestParseDescriptorRefusesWhatTheHubWould(t *testing.T) {
	cases := map[string]string{
		"no service":   "category: Cost\n",
		"unknown key":  "service: s\nowner: someone\n",
		"bad kind":     "service: s\nprovides: [{id: a, title: A, kind: soap}]\n",
		"no api id":    "service: s\nprovides: [{title: A, kind: rest}]\n",
		"api repeated": "service: s\nprovides: [{id: a, title: A, kind: rest}, {id: a, title: B, kind: rest}]\n",
		"spec rooted":  "service: github/a/b\nprovides: [{id: a, title: A, kind: rest, spec: /openapi.json}]\n",
		"spec escapes": "service: github/a/b\nprovides: [{id: a, title: A, kind: rest, spec: ../x.json}]\n",
		"spec no repo": "service: s\nprovides: [{id: a, title: A, kind: rest, spec: openapi.json}]\n",
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
		if r.URL.Path == "/v1/ingest/blueprints" {
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
	if writes[0].method != "PUT" || writes[0].path != "/v1/ingest/blueprints" {
		t.Errorf("first write %s %s, want the blueprint offer", writes[0].method, writes[0].path)
	}
	svc := writes[1]
	if svc.method != "PATCH" || svc.path != "/v1/ingest/entities/sw_service/github/acme/tally/cmd/tally" {
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
	if api.method != "PUT" || api.path != "/v1/ingest/entities/sw_api/github/acme/tally/cmd/tally/costs" {
		t.Errorf("api written with %s %s", api.method, api.path)
	}
	if api.body["relations"].(map[string]any)["service"] != "github/acme/tally/cmd/tally" {
		t.Errorf("api not tied to its service: %v", api.body)
	}
	if got := api.body["fields"].(map[string]any)["spec"]; got != "https://github.com/acme/tally/blob/HEAD/api/openapi.yaml" {
		t.Errorf("spec = %v, want the file's link on the default branch", got)
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

func TestDescriberLogsTheClientsLinesThroughTheAppsLogger(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/ingest/blueprints" {
			json.NewEncoder(w).Encode(map[string]any{"created": []string{}, "extended": []string{}, "kept": []string{"sw_service", "sw_api"}})
			return
		}
		w.Write([]byte(`{"outcome":"updated"}`))
	}))
	defer srv.Close()
	d, _ := ParseDescriptor([]byte(descriptorYAML))
	var app bytes.Buffer
	r, err := NewDescriber(New(srv.URL, "tok", WithRetries(0)), d, "tally", slog.New(slog.NewJSONHandler(&app, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.once(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, msg := range []string{"orrery blueprints offered", "orrery described"} {
		if !strings.Contains(app.String(), `"msg":"`+msg+`"`) {
			t.Errorf("app log lacks %q:\n%s", msg, app.String())
		}
	}
}

func TestSpecURLPlacesARepositoryFileFromTheRootOnTheDefaultBranch(t *testing.T) {
	cases := []struct{ service, spec, want string }{
		{"github/acme/tally/cmd/tally", "openapi.yaml", "https://github.com/acme/tally/blob/HEAD/openapi.yaml"},
		{"github/acme/mono/apps/web", "apps/web/api/openapi.yaml", "https://github.com/acme/mono/blob/HEAD/apps/web/api/openapi.yaml"},
		{"internal/billing", "https://billing.example.test/openapi.json", "https://billing.example.test/openapi.json"},
		{"github/acme/tally", "", ""},
	}
	for _, c := range cases {
		got, err := SpecURL(c.service, c.spec)
		if err != nil || got != c.want {
			t.Errorf("SpecURL(%q, %q) = %q, %v; want %q", c.service, c.spec, got, err, c.want)
		}
	}
}
