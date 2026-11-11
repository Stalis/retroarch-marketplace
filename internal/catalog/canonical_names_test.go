package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type boxartTransport struct{ url string }

func (t boxartTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "thumbnails.libretro.com" || r.URL.Path != "/Test System/Named_Boxarts/" {
		return nil, fmt.Errorf("unexpected URL %s", r.URL)
	}
	req := r.Clone(r.Context())
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(t.url, "http://")
	return http.DefaultTransport.RoundTrip(req)
}

func TestCanonicalNames(t *testing.T) {
	listing := `<html><body><a href="Game%20The.png">x</a><a href="The%20Game.png">x</a><a href="The%20Games.png">x</a><a href="ignore.txt">x</a>`
	for i := range 12 {
		listing += fmt.Sprintf(`<a href="Other%d.png">x</a>`, i)
	}
	listing += `</body></html>`
	status := 200
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status); io.WriteString(w, listing) }))
	defer upstream.Close()
	old := boxartClient
	boxartClient = &http.Client{Transport: boxartTransport{upstream.URL}}
	defer func() { boxartClient = old }()
	st, packages := fixture(t, "[]", nil)
	h, err := New(st, packages)
	if err != nil {
		t.Fatal(err)
	}
	target := "/api/admin/canonical-names?platform=Test+System&q=The+Game"
	w := adminRequest(h, "GET", target, nil, "")
	var names []string
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &names) != nil || len(names) != 10 || names[0] != "The Game" || names[1] != "Game The" {
		t.Fatalf("lookup: %d %s", w.Code, w.Body.String())
	}
	for _, target := range []string{"/api/admin/canonical-names?q=x", "/api/admin/canonical-names?platform=..&q=x", "/api/admin/canonical-names?platform=a%2Fb&q=x", "/api/admin/canonical-names?platform=x"} {
		if w := adminRequest(h, "GET", target, nil, ""); w.Code != 400 {
			t.Fatalf("invalid %s: %d", target, w.Code)
		}
	}
	r := httptest.NewRequest("GET", "http://localhost"+target, nil)
	r.RemoteAddr = "192.0.2.1:1234"
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("remote: %d", w.Code)
	}
	status = 500
	if w := adminRequest(h, "GET", target, nil, ""); w.Code != 200 {
		t.Fatalf("cached listing: %d", w.Code)
	}
	// Later cases need fresh fetches; drop the cached listing before each one.
	purge := func() {
		if _, err := st.PurgeBoxart(context.Background(), time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	purge()
	status = 500
	if w := adminRequest(h, "GET", target, nil, ""); w.Code != 502 {
		t.Fatalf("upstream: %d", w.Code)
	}
	status = 200
	listing = `<html><body><a href="Rock%20&amp;%20Roll.png">x</a><a href="other.jpg">x</a></body></html>`
	purge()
	w = adminRequest(h, "GET", target, nil, "")
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &names) != nil || len(names) != 1 || names[0] != "Rock & Roll" {
		t.Fatalf("encoded name: %d %s", w.Code, w.Body.String())
	}
	listing = `<html><body><a href="other.jpg">x</a></body></html>`
	purge()
	w = adminRequest(h, "GET", target, nil, "")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("empty: %d %s", w.Code, w.Body.String())
	}
	listing = `<html><body><a href="partial.png">x</a>`
	purge()
	if w := adminRequest(h, "GET", target, nil, ""); w.Code != 502 {
		t.Fatalf("truncated: %d %s", w.Code, w.Body.String())
	}
	status = 200
	listing = "not a listing"
	purge()
	if w := adminRequest(h, "GET", target, nil, ""); w.Code != 502 {
		t.Fatalf("malformed: %d", w.Code)
	}
	listing = strings.Repeat("x", (8<<20)+1)
	purge()
	if w := adminRequest(h, "GET", target, nil, ""); w.Code != 502 {
		t.Fatalf("oversized: %d", w.Code)
	}
}

func TestCanonicalNamesWithMaxDistance(t *testing.T) {
	st, packages := fixture(t, "[]", nil)
	names := []string{"Darkstone (Australia)", "Darkstones (USA)", "Dark Seed"}
	if err := st.ReplaceBoxart(context.Background(), "Test System", names, time.Now()); err != nil {
		t.Fatal(err)
	}
	limit := 0
	h, err := NewWithBoxArtDistance(st, packages, &limit)
	if err != nil {
		t.Fatal(err)
	}
	w := adminRequest(h, "GET", "/api/admin/canonical-names?platform=Test+System&q=DarkStone", nil, "")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `["Darkstone (Australia)"]` {
		t.Fatalf("lookup: %d %s", w.Code, w.Body.String())
	}
}
