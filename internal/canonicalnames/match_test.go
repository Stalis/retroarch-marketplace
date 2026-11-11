package canonicalnames

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"retroarch-marketplace/internal/store"
)

var listingNames = []string{"Tekken 3 (USA)", "Tekken 3 (Europe, Australia)", "Tekken (Europe)", "Final Fantasy VII (USA) (Disc 1)", "Metal Gear Solid (USA) (Disc 1)"}

func TestMatchFile(t *testing.T) {
	for file, want := range map[string]string{
		"Tekken3.cue":                       "Tekken 3 (USA)",
		"final_fantasy_vii_disc1.bin":       "Final Fantasy VII (USA) (Disc 1)",
		"Metal.Gear.Solid.[SLUS-00594].cue": "Metal Gear Solid (USA) (Disc 1)",
		"Tekken 3 (E).bin":                  "Tekken 3 (Europe, Australia)",
		"Foo Bar.bin":                       "",
	} {
		got := MatchFile(file, listingNames)
		if (want == "" && len(got) != 0) || (want != "" && (len(got) == 0 || got[0] != want)) {
			t.Errorf("%s: %v", file, got)
		}
	}
}

func TestTitleAndDisc(t *testing.T) {
	for file, want := range map[string]struct {
		title string
		disc  int
	}{
		"dir/Two (USA) (Disc 2).cue": {"Two (USA)", 2},
		"ff7_disc1.bin":              {"ff7", 1},
		"Game (Disc 1 of 3).chd":     {"Game", 1},
		"Plain.iso":                  {"Plain", 0},
	} {
		n, _ := DiscNumber(file)
		if got := Title(file); got != want.title || n != want.disc {
			t.Errorf("%s: %q %d", file, got, n)
		}
	}
	if got := Tokens("Tekken3 III [b] (USA)"); !reflect.DeepEqual(got, map[string]bool{"tekken": true, "3": true}) {
		t.Errorf("tokens: %v", got)
	}
}

type transport struct{ url string }

func (t transport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "thumbnails.libretro.com" || r.URL.Path != "/Test System/Named_Boxarts/" {
		return nil, fmt.Errorf("unexpected URL %s", r.URL)
	}
	req := r.Clone(r.Context())
	req.URL.Scheme = "http"
	req.URL.Host = strings.TrimPrefix(t.url, "http://")
	return http.DefaultTransport.RoundTrip(req)
}

func TestFinderCache(t *testing.T) {
	ctx := context.Background()
	var hits atomic.Int32
	status := 200
	listing := "<html><body>"
	for _, name := range listingNames {
		listing += `<a href="` + strings.ReplaceAll(name, " ", "%20") + `.png">x</a>`
	}
	listing += "</body></html>"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(status)
		io.WriteString(w, listing)
	}))
	defer upstream.Close()
	st, err := store.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Unix(1_000_000, 0)
	f := Finder{Store: st, Client: &http.Client{Transport: transport{upstream.URL}}, Now: func() time.Time { return now }}
	for range 2 {
		if names, err := f.Titles(ctx, "Test System", "Tekken 3"); err != nil || names[0] != "Tekken 3 (USA)" {
			t.Fatal(names, err)
		}
	}
	if names, err := f.File(ctx, "Test System", "Tekken3.cue"); err != nil || len(names) != 2 || names[0] != "Tekken 3 (USA)" {
		t.Fatal(names, err)
	}
	if hits.Load() != 1 {
		t.Fatalf("hits within TTL: %d", hits.Load())
	}
	now = now.Add(8 * 24 * time.Hour)
	if _, err := f.Titles(ctx, "Test System", "Tekken"); err != nil || hits.Load() != 2 {
		t.Fatalf("refetch: %v %d", err, hits.Load())
	}
	now = now.Add(8 * 24 * time.Hour)
	status = 500
	if _, err := f.Titles(ctx, "Test System", "Tekken"); err == nil {
		t.Fatal("served an expired listing")
	}
	if n, err := st.PurgeBoxart(ctx, now.Add(-TTL)); err != nil || n != 1 {
		t.Fatalf("purge: %d %v", n, err)
	}
	if _, ok, _ := st.BoxartNames(ctx, "Test System", time.Time{}); ok {
		t.Fatal("purged listing still cached")
	}
	if _, err := f.Titles(ctx, "..", "x"); err != ErrInvalidInput {
		t.Fatalf("invalid platform: %v", err)
	}
}
