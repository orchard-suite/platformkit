package orrery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

const svgLogo = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"><rect width="1" height="1"/></svg>`

const iconYAML = "service: github/acme/tally/cmd/tally\nicon: {name: tally, file: assets/logo.svg}\n"

func TestParseDescriptorFSReadsTheIcon(t *testing.T) {
	fsys := fstest.MapFS{
		"orrery.yaml":     {Data: []byte(iconYAML)},
		"assets/logo.svg": {Data: []byte(svgLogo)},
	}
	d, err := ParseDescriptorFS(fsys, "orrery.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if d.Icon == nil || d.Icon.Name != "tally" || d.Icon.Type != "image/svg+xml" || string(d.Icon.Data) != svgLogo {
		t.Fatalf("icon %+v", d.Icon)
	}
	if got := d.Entities()[0].Icon; got != "tally" {
		t.Errorf("service icon %q, want the icon's name", got)
	}
}

func TestParseDescriptorFSWithoutAnIcon(t *testing.T) {
	d, err := ParseDescriptorFS(fstest.MapFS{"orrery.yaml": {Data: []byte(descriptorYAML)}}, "orrery.yaml")
	if err != nil || d.Icon != nil || d.Entities()[0].Icon != "" {
		t.Fatalf("%+v %v", d, err)
	}
}

func TestParseDescriptorFSRefusesABadIcon(t *testing.T) {
	cases := map[string]fstest.MapFS{
		"no file":     {"orrery.yaml": {Data: []byte("service: s\nicon: {name: tally}\n")}},
		"no name":     {"orrery.yaml": {Data: []byte("service: s\nicon: {file: logo.svg}\n")}, "logo.svg": {Data: []byte(svgLogo)}},
		"empty":       {"orrery.yaml": {Data: []byte("service: s\nicon: {}\n")}},
		"bad name":    {"orrery.yaml": {Data: []byte("service: s\nicon: {name: Tally-App, file: logo.svg}\n")}, "logo.svg": {Data: []byte(svgLogo)}},
		"unknown key": {"orrery.yaml": {Data: []byte("service: s\nicon: {name: tally, file: logo.svg, size: 3}\n")}, "logo.svg": {Data: []byte(svgLogo)}},
		"bad type":    {"orrery.yaml": {Data: []byte("service: s\nicon: {name: tally, file: logo.gif}\n")}, "logo.gif": {Data: []byte("GIF89a")}},
		"missing":     {"orrery.yaml": {Data: []byte("service: s\nicon: {name: tally, file: logo.svg}\n")}},
		"too big":     {"orrery.yaml": {Data: []byte("service: s\nicon: {name: tally, file: logo.png}\n")}, "logo.png": {Data: make([]byte, maxIconSize+1)}},
	}
	for name, fsys := range cases {
		if _, err := ParseDescriptorFS(fsys, "orrery.yaml"); err == nil {
			t.Errorf("%s: parsed, want an error", name)
		}
	}
	at := fstest.MapFS{"orrery.yaml": {Data: []byte("service: s\nicon: {name: tally, file: logo.webp}\n")}, "logo.webp": {Data: make([]byte, maxIconSize)}}
	if d, err := ParseDescriptorFS(at, "orrery.yaml"); err != nil || d.Icon.Type != "image/webp" {
		t.Errorf("an icon of exactly the limit: %v", err)
	}
}

func TestParseDescriptorPointsAnIconToTheFS(t *testing.T) {
	_, err := ParseDescriptor([]byte(iconYAML))
	if err == nil || !strings.Contains(err.Error(), "ParseDescriptorFS") {
		t.Fatalf("err = %v, want one naming ParseDescriptorFS", err)
	}
}

func TestDescribeUploadsTheIconThenSetsIt(t *testing.T) {
	var order []string
	var uploaded []byte
	var uploadType string
	var serviceIcon any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, r.Method+" "+r.URL.Path)
		switch {
		case r.URL.Path == "/v1/icons/tally":
			uploaded, _ = io.ReadAll(r.Body)
			uploadType = r.Header.Get("Content-Type")
			w.Write([]byte(`{"name":"tally","type":"image/svg+xml","sha256":"x","size":1}`))
		case r.URL.Path == "/v1/ingest/blueprints":
			json.NewEncoder(w).Encode(map[string]any{"kept": []string{"sw_service", "sw_api"}})
		default:
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			serviceIcon = body["icon"]
			w.Write([]byte(`{"outcome":"updated"}`))
		}
	}))
	defer srv.Close()
	d, err := ParseDescriptorFS(fstest.MapFS{"orrery.yaml": {Data: []byte(iconYAML)}, "assets/logo.svg": {Data: []byte(svgLogo)}}, "orrery.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := New(srv.URL, "tok", WithRetries(0)).Describe(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if len(order) != 3 || order[0] != "PUT /v1/icons/tally" || order[2] != "PATCH /v1/ingest/entities/sw_service/github/acme/tally/cmd/tally" {
		t.Fatalf("calls %v, want the icon first and the service patched", order)
	}
	if string(uploaded) != svgLogo || uploadType != "image/svg+xml" {
		t.Errorf("uploaded %q as %q", uploaded, uploadType)
	}
	if serviceIcon != "tally" {
		t.Errorf("service icon %v, want tally", serviceIcon)
	}
}

func TestDescribeStopsAtARefusedIcon(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"error":"svg holds a script"}`))
	}))
	defer srv.Close()
	d, _ := ParseDescriptorFS(fstest.MapFS{"orrery.yaml": {Data: []byte(iconYAML)}, "assets/logo.svg": {Data: []byte(svgLogo)}}, "orrery.yaml")
	err := New(srv.URL, "tok", WithRetries(0)).Describe(context.Background(), d)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 422 || calls != 1 {
		t.Fatalf("err %v after %d calls, want the 422 and nothing else written", err, calls)
	}
}

func TestIconReadsRevalidatesAndMisses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer t" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/v1/icons/tally":
			w.Header().Set("ETag", `"abc"`)
			if r.Header.Get("If-None-Match") == `"abc"` {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("Content-Type", "image/svg+xml")
			w.Write([]byte(svgLogo))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(srv.URL, "t", WithRetries(0))
	img, err := c.Icon(t.Context(), "tally", "")
	if err != nil || img.NotModified || img.Type != "image/svg+xml" || img.ETag != `"abc"` || string(img.Data) != svgLogo {
		t.Fatalf("%+v %v", img, err)
	}
	img, err = c.Icon(t.Context(), "tally", `"abc"`)
	if err != nil || !img.NotModified || img.ETag != `"abc"` || img.Data != nil {
		t.Fatalf("revalidate: %+v %v", img, err)
	}
	if _, err := c.Icon(t.Context(), "missing", ""); !errors.Is(err, ErrIconNotFound) {
		t.Fatalf("err = %v, want ErrIconNotFound", err)
	}
}

func TestDescriberLogsTheIcon(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	d, _ := ParseDescriptorFS(fstest.MapFS{"orrery.yaml": {Data: []byte(iconYAML)}, "assets/logo.svg": {Data: []byte(svgLogo)}}, "orrery.yaml")
	var app bytes.Buffer
	r, err := NewDescriber(New(srv.URL, "tok", WithRetries(0)), d, "tally", slog.New(slog.NewJSONHandler(&app, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.once(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(app.String(), `"icon":"tally"`) {
		t.Errorf("describe log lacks the icon:\n%s", app.String())
	}
}

func TestIconForAsksForTheTheme(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.RequestURI())
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Write([]byte(svgLogo))
	}))
	defer srv.Close()
	c := New(srv.URL, "t", WithRetries(0))
	if _, err := c.IconFor(t.Context(), "github", ThemeLight, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Icon(t.Context(), "github", ""); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "/v1/icons/github?theme=light" || got[1] != "/v1/icons/github" {
		t.Fatalf("requests %v", got)
	}
}
