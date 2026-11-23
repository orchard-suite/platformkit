package faro

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/orchard-suite/platformkit/door"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestOwnStampsAppAndPerson(t *testing.T) {
	in := `{"meta":{"app":{"name":"evil","version":"0"},"user":{"email":"forged@example.com"}},"events":[]}`
	out, ok := Own([]byte(in), "app-web", "1.0.0", "a@example.com")
	if !ok {
		t.Fatal("refused")
	}
	var b struct {
		Meta struct {
			App  map[string]string `json:"app"`
			User map[string]string `json:"user"`
		} `json:"meta"`
		Events []any `json:"events"`
	}
	json.Unmarshal(out, &b)
	if b.Meta.App["name"] != "app-web" || b.Meta.App["version"] != "1.0.0" || b.Meta.User["email"] != "a@example.com" || b.Events == nil {
		t.Fatalf("%s", out)
	}
	out, _ = Own([]byte(in), "app-web", "1.0.0", "")
	if strings.Contains(string(out), "forged") {
		t.Fatalf("a batch with nobody signed in kept a user: %s", out)
	}
	for _, bad := range []string{"[]", "null", "x"} {
		if _, ok := Own([]byte(bad), "a", "1", ""); ok {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestRelay(t *testing.T) {
	var got []byte
	var key string
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		key = r.Header.Get("X-API-Key")
	}))
	defer collector.Close()
	post := func(r *Relay, body string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/telemetry", strings.NewReader(body))
		req = req.WithContext(door.With(req.Context(), door.Person{Email: "a@example.com"}))
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		return rec.Code
	}
	r, err := NewRelay(Config{Service: "app", Version: "1", URL: collector.URL, Key: "k"}, quiet())
	if err != nil {
		t.Fatal(err)
	}
	if code := post(r, `{"meta":{}}`); code != http.StatusAccepted || key != "k" || !strings.Contains(string(got), `"a@example.com"`) {
		t.Fatalf("%d %q %s", code, key, got)
	}
	if code := post(r, strings.Repeat("x", MaxBatchBytes+1)); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("too large: %d", code)
	}
	if code := post(r, "nope"); code != http.StatusBadRequest {
		t.Fatalf("not JSON: %d", code)
	}
	got = nil
	keyless, _ := NewRelay(Config{Service: "app", Version: "1", URL: collector.URL}, quiet())
	if code := post(keyless, `{}`); code != http.StatusAccepted || got != nil {
		t.Fatalf("without a key the batch is dropped: %d %s", code, got)
	}
}
