package orrery

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

const svgLogo = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"><rect width="1" height="1"/></svg>`

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
