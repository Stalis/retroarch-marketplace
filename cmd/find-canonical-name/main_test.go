package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

type fixtureTransport struct{ url string }

func (t fixtureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "thumbnails.libretro.com" || r.URL.Path != "/Test System/Named_Boxarts/" {
		return nil, fmt.Errorf("unexpected URL: %s", r.URL)
	}
	req := r.Clone(r.Context())
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(t.url, "http://")
	return http.DefaultTransport.RoundTrip(req)
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, fmt.Errorf("write failed") }

func TestRun(t *testing.T) {
	listing := `<html><body><a href="Game%20The.png">x</a><a href="The%20Game.png">x</a><a href="The%20Games.png">x</a><a href="Rock%20%26%20Roll.png">x</a><a href="ignore.jpg">x</a>`
	for i := range 12 {
		listing += fmt.Sprintf(`<a href="Other%d.png">x</a>`, i)
	}
	listing += `</body></html>`
	status := 200
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status); io.WriteString(w, listing) }))
	defer server.Close()
	client := &http.Client{Transport: fixtureTransport{server.URL}}
	args := []string{"-platform", "Test System", "-q", "The Game"}
	var out, errout bytes.Buffer
	if code := run(args, &out, &errout, client); code != 0 {
		t.Fatalf("code %d: %s", code, &errout)
	}
	var names []string
	if err := json.Unmarshal(out.Bytes(), &names); err != nil || len(names) != 10 || !reflect.DeepEqual(names[:3], []string{"The Game", "Game The", "The Games"}) {
		t.Fatalf("ranked output: %s (%v)", &out, err)
	}
	listing = `<html><body><a href="Rock%20%26%20Roll.png">x</a><a href="ignore.jpg">x</a></body></html>`
	out.Reset()
	errout.Reset()
	if code := run(args, &out, &errout, client); code != 0 || json.Unmarshal(out.Bytes(), &names) != nil || !reflect.DeepEqual(names, []string{"Rock & Roll"}) {
		t.Fatalf("encoded: %d %s %s", code, &out, &errout)
	}
	listing = `<html><body><a href="ignore.jpg">x</a></body></html>`
	out.Reset()
	errout.Reset()
	if code := run(args, &out, &errout, client); code != 0 || out.String() != "[]\n" {
		t.Fatalf("empty: %d %s %s", code, &out, &errout)
	}
	for _, invalid := range [][]string{nil, {"-platform", "../bad", "-q", "x"}, {"-platform", "Test System"}, {"-bad"}, {"-platform", "Test System", "-q", "x", "extra"}} {
		out.Reset()
		errout.Reset()
		if code := run(invalid, &out, &errout, client); code != 2 || out.Len() != 0 || errout.Len() == 0 {
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
		if code := run(args, &out, &errout, client); code != 1 || out.Len() != 0 || errout.Len() == 0 {
			t.Fatalf("failure: %d %s %s", code, &out, &errout)
		}
	}
	status, listing = 200, `<html></html>`
	errout.Reset()
	if code := run(args, failingWriter{}, &errout, client); code != 1 || errout.Len() == 0 {
		t.Fatalf("output error: %d %s", code, &errout)
	}
}
