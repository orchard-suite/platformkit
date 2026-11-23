package door

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func serve(t *testing.T, g *Gate, headers map[string]string) (*httptest.ResponseRecorder, Person) {
	t.Helper()
	var got Person
	mux := http.NewServeMux()
	mux.Handle("GET /x", g.Func(func(w http.ResponseWriter, r *http.Request) { got = From(r.Context()) }))
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec, got
}

func TestGateAdmitsAnyListedRole(t *testing.T) {
	g, err := NewGate(quiet(), "Reader", "Admin")
	if err != nil {
		t.Fatal(err)
	}
	rec, p := serve(t, g, map[string]string{HeaderEmail: "a@example.com", HeaderGroups: " Viewer , Admin"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if p.Email != "a@example.com" || !p.Has("Admin") || p.Has("Reader") {
		t.Fatalf("person %+v", p)
	}
}

func TestGateRefusesWithoutIdentityOrRole(t *testing.T) {
	g, _ := NewGate(quiet(), "Admin")
	if rec, _ := serve(t, g, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no identity: %d", rec.Code)
	}
	if rec, _ := serve(t, g, map[string]string{HeaderEmail: "a@example.com", HeaderGroups: "Reader"}); rec.Code != http.StatusForbidden {
		t.Fatalf("no role: %d", rec.Code)
	}
	if rec, _ := serve(t, g, map[string]string{HeaderEmail: "a@example.com", HeaderGroups: "ledger.Admin"}); rec.Code != http.StatusForbidden {
		t.Fatalf("a prefixed role is not the role: %d", rec.Code)
	}
}

func TestGateWithoutRolesAdmitsAnyoneSignedIn(t *testing.T) {
	g, _ := NewGate(quiet())
	if rec, _ := serve(t, g, map[string]string{HeaderEmail: "a@example.com"}); rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestNewGateRefusesPrefixedRole(t *testing.T) {
	for _, role := range []string{"ledger.Admin", "", "A,B", "Some Role"} {
		if _, err := NewGate(quiet(), role); err == nil {
			t.Fatalf("%q was accepted", role)
		}
	}
}

func TestRefuseWritesTheAppsOwnPage(t *testing.T) {
	g, _ := NewGate(quiet(), "Admin")
	g.Refuse = func(w http.ResponseWriter, _ *http.Request, status int, message string) {
		w.WriteHeader(status)
		io.WriteString(w, "page: "+message)
	}
	rec, _ := serve(t, g, map[string]string{HeaderEmail: "a@example.com"})
	if rec.Code != http.StatusForbidden || rec.Body.String() != "page: This is not yours to open." {
		t.Fatalf("%d %q", rec.Code, rec.Body.String())
	}
}

func TestSameOrigin(t *testing.T) {
	h := SameOrigin("https://app.example.org", quiet(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	for origin, want := range map[string]int{"https://app.example.org": 200, "https://other.example.org": 403, "": 403} {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Fatalf("%q: %d, want %d", origin, rec.Code, want)
		}
	}
}
