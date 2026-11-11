package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type fixtureTransport struct{ url string }

func (t fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "thumbnails.libretro.com" || (r.URL.Path != "/Test System/Named_Boxarts/" && r.URL.Path != "/Sony - PlayStation/Named_Boxarts/") {
		return nil, fmt.Errorf("unexpected URL: %s", r.URL)
	}
	req := r.Clone(r.Context())
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(t.url, "http://")
	return http.DefaultTransport.RoundTrip(req)
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, fmt.Errorf("write failed") }

// useBoxart routes BoxArt lookups to a test server serving *listing with *status.
func useBoxart(t *testing.T, status *int, listing *string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(*status); io.WriteString(w, *listing) }))
	t.Cleanup(server.Close)
	old := boxartClient
	boxartClient = &http.Client{Transport: fixtureTransport{server.URL}}
	t.Cleanup(func() { boxartClient = old })
	isolateConfig(t)
}

// isolateConfig points RETRO_CONFIG at an empty file so a developer's config.toml cannot leak in.
func isolateConfig(t *testing.T) {
	t.Helper()
	empty := filepath.Join(t.TempDir(), "empty.toml")
	if err := os.WriteFile(empty, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("RETRO_CONFIG", empty)
}

func TestFindName(t *testing.T) {
	listing := `<html><body><a href="Game%20The.png">x</a><a href="The%20Game.png">x</a><a href="The%20Games.png">x</a><a href="Rock%20%26%20Roll.png">x</a><a href="ignore.jpg">x</a>`
	for i := range 12 {
		listing += fmt.Sprintf(`<a href="Other%d.png">x</a>`, i)
	}
	listing += `</body></html>`
	status := 200
	useBoxart(t, &status, &listing)
	// Each case uses a fresh database so the listing is fetched again.
	findName := func(stdout io.Writer, stderr io.Writer, args ...string) int {
		db := filepath.Join(t.TempDir(), "catalog.db")
		return run(append([]string{"find-name", "-database", db}, args...), stdout, stderr)
	}
	args := []string{"-platform", "Test System", "-q", "The Game"}
	var out, errout bytes.Buffer
	if code := findName(&out, &errout, args...); code != 0 {
		t.Fatalf("code %d: %s", code, &errout)
	}
	var names []string
	if err := json.Unmarshal(out.Bytes(), &names); err != nil || len(names) != 10 || !reflect.DeepEqual(names[:3], []string{"The Game", "Game The", "The Games"}) {
		t.Fatalf("ranked output: %s (%v)", &out, err)
	}
	out.Reset()
	if code := findName(&out, &errout, "-platform", "Test System", "-file", "roms/Rock_and_Roll.zip"); code != 0 || json.Unmarshal(out.Bytes(), &names) != nil || !reflect.DeepEqual(names, []string{"Rock & Roll"}) {
		t.Fatalf("file: %d %s %s", code, &out, &errout)
	}
	listing = `<html><body><a href="ignore.jpg">x</a></body></html>`
	out.Reset()
	errout.Reset()
	if code := findName(&out, &errout, args...); code != 0 || out.String() != "[]\n" {
		t.Fatalf("empty: %d %s %s", code, &out, &errout)
	}
	for _, invalid := range [][]string{nil, {"-platform", "../bad", "-q", "x"}, {"-platform", "Test System"}, {"-bad"}, {"-platform", "Test System", "-q", "x", "extra"},
		{"-platform", "Test System", "-q", "x", "-file", "x.bin"}, {"-platform", "Test System", "-q", "  "}} {
		out.Reset()
		errout.Reset()
		if code := findName(&out, &errout, invalid...); code != 2 || out.Len() != 0 || errout.Len() == 0 {
			t.Fatalf("invalid %v: %d %s %s", invalid, code, &out, &errout)
		}
	}
	for _, broken := range []struct {
		status  int
		listing string
	}{{500, ""}, {200, "<html>truncated"}, {200, `<html><a href="%zz.png">x</a></html>`}} {
		status, listing = broken.status, broken.listing
		out.Reset()
		errout.Reset()
		if code := findName(&out, &errout, args...); code != 1 || out.Len() != 0 || errout.Len() == 0 {
			t.Fatalf("failure: %d %s %s", code, &out, &errout)
		}
	}
	status, listing = 200, `<html></html>`
	errout.Reset()
	if code := findName(failingWriter{}, &errout, args...); code != 1 || errout.Len() == 0 {
		t.Fatalf("output error: %d %s", code, &errout)
	}
}

func TestRunDispatch(t *testing.T) {
	var out, errout bytes.Buffer
	for args, want := range map[string]int{"": 2, "help": 0, "-h": 0, "bogus": 2, "version": 0} {
		out.Reset()
		errout.Reset()
		var argv []string
		if args != "" {
			argv = []string{args}
		}
		if code := run(argv, &out, &errout); code != want {
			t.Errorf("%q: %d", args, code)
		}
	}
	out.Reset()
	if code := run([]string{"version"}, &out, &errout); code != 0 || out.String() != "content-catalog dev\n" {
		t.Fatalf("version: %q", out.String())
	}
}
