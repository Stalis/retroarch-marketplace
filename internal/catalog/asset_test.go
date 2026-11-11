package catalog

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func assetLabel(s string) string {
	return "~" + hex.EncodeToString([]byte(s))
}

func assetIndex(t *testing.T, h http.Handler, target string) []string {
	t.Helper()
	w := request(t, h, http.MethodGet, target)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s: %d %s", target, w.Code, w.Body.String())
	}
	body := w.Body.String()
	if body == "" {
		return []string{}
	}
	if !strings.HasSuffix(body, "\n") {
		t.Fatalf("index %s missing final newline: %q", target, body)
	}
	return strings.Split(strings.TrimSuffix(body, "\n"), "\n")
}

func TestAssetDownloader(t *testing.T) {
	const platform = "Sony - PlayStation"
	const unusualPlatform = "平台; 100% #"
	ids := []string{"first game", "second game", "游戏; 100% #"}
	names := []string{"Castlevania: Symphony of the Night", ids[1], ids[2]}
	platforms := []string{platform, platform, unusualPlatform}
	contents := [][]zipEntry{
		{{name: "game.m3u", data: "Disc/game.cue\n"}, {name: "Disc/game.cue", data: "FILE \"game.bin\" BINARY\n"}, {name: "Disc/game.bin", data: "first disc"}},
		{{name: "game.m3u", data: "Disc/game.cue\n"}, {name: "Disc/game.cue", data: "FILE \"game.bin\" BINARY\n"}, {name: "Disc/game.bin", data: "second disc"}},
		{{name: "game.m3u", data: "Disc/game.cue\n"}, {name: "Disc/game.cue", data: "FILE \"game.bin\" BINARY\n"}, {name: "Disc/game.bin", data: "third disc"}},
	}
	var cards []map[string]interface{}
	packages := make(map[string][]zipEntry)
	for i, id := range ids {
		cards = append(cards, map[string]interface{}{
			"id": id, "canonical_name": names[i],
			"platform":   map[string]string{"id": "psx", "name": "PlayStation", "libretro_name": platforms[i]},
			"entrypoint": "game.m3u", "files": []string{"game.m3u", "Disc/game.cue", "Disc/game.bin"}, "download_url": "original.zip",
		})
		packages[id] = contents[i]
	}
	manifest, err := json.Marshal(cards)
	if err != nil {
		t.Fatal(err)
	}
	catalog, dir := fixture(t, string(manifest), packages)
	h, err := New(catalog, dir)
	if err != nil {
		t.Fatal(err)
	}
	categories := assetIndex(t, h, "/assets/cores/.index-dirs")
	wantCategories := []string{platform, assetLabel(unusualPlatform)}
	if !reflect.DeepEqual(categories, wantCategories) {
		t.Fatalf("categories %q, want %q", categories, wantCategories)
	}
	labels := []string{names[0], "second game", assetLabel(ids[2])}
	for i, category := range categories {
		base := "/assets/cores/" + url.PathEscape(category)
		redirect := request(t, h, http.MethodGet, base)
		if redirect.Code != http.StatusMovedPermanently || redirect.Header().Get("Location") != base+"/" {
			t.Fatalf("redirect %s: %d Location=%q", base, redirect.Code, redirect.Header().Get("Location"))
		}
		want := []string{labels[0] + ".zip", labels[1] + ".zip"}
		if i == 1 {
			want = []string{labels[2] + ".zip"}
		}
		index := assetIndex(t, h, base+"/.index")
		if !reflect.DeepEqual(index, want) {
			t.Fatalf("index %s: %q, want %q", base, index, want)
		}
		if got := request(t, h, http.MethodGet, base+"/.index-dirs").Code; got != http.StatusNotFound {
			t.Errorf("subdirectories %s: %d", base, got)
		}
	}
	if entries := cacheEntries(t, dir); len(entries) != 0 {
		t.Fatalf("indexes created archives: %v", entries)
	}

	allNames := make(map[string]bool)
	for i, id := range ids {
		category := platform
		if i == 2 {
			category = assetLabel(unusualPlatform)
		}
		target := "/assets/cores/" + url.PathEscape(category) + "/" + url.PathEscape(labels[i]+".zip")
		w := request(t, h, http.MethodGet, target)
		if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/zip") {
			t.Fatalf("download %s: %d %v", target, w.Code, w.Header())
		}
		if i == 0 {
			if entries := cacheEntries(t, dir); len(entries) != 1 {
				t.Fatalf("asset download cache: %v", entries)
			}
			api := request(t, h, http.MethodGet, "/api/packages/"+url.PathEscape(id)+".zip")
			if api.Code != http.StatusOK || len(cacheEntries(t, dir)) != 1 {
				t.Fatalf("API did not reuse asset cache: %d", api.Code)
			}
		}
		zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
		if err != nil {
			t.Fatal(err)
		}
		files := make(map[string]string)
		for _, f := range zr.File {
			if allNames[f.Name] {
				t.Fatalf("archives collide at %q", f.Name)
			}
			allNames[f.Name] = true
			r, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(r)
			if err != nil {
				t.Fatal(err)
			}
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
			files[f.Name] = string(data)
		}
		if len(files) != len(contents[i]) {
			t.Fatalf("archive %s: unexpected members %v", id, files)
		}
		prefix := labels[i] + "/"
		for _, original := range contents[i] {
			if got, ok := files[prefix+original.name]; !ok || got != original.data {
				t.Fatalf("archive %s missing or altered %q: %q", id, prefix+original.name, got)
			}
		}
		m3u := prefix + "game.m3u"
		cue := path.Join(path.Dir(m3u), strings.TrimSpace(files[m3u]))
		cueText, ok := files[cue]
		if !ok || !strings.HasPrefix(cueText, "FILE \"") {
			t.Fatalf("M3U target missing: %q", cue)
		}
		binName := strings.SplitN(strings.TrimPrefix(cueText, "FILE \""), "\"", 2)[0]
		if _, ok := files[path.Join(path.Dir(cue), binName)]; !ok {
			t.Fatalf("CUE target missing: %q", binName)
		}
		post := request(t, h, http.MethodPost, target)
		if post.Code != http.StatusMethodNotAllowed || post.Header().Get("Allow") != "GET" {
			t.Errorf("POST %s: %d Allow=%q", target, post.Code, post.Header().Get("Allow"))
		}
	}
	for _, target := range []string{"/assets/cores/.index-dirs", "/assets/cores/" + url.PathEscape(platform), "/assets/cores/" + url.PathEscape(platform) + "/.index"} {
		w := request(t, h, http.MethodPost, target)
		if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "GET" {
			t.Errorf("POST %s: %d Allow=%q", target, w.Code, w.Header().Get("Allow"))
		}
	}
	for _, target := range []string{"/assets/", "/assets/cores/", "/assets/cores/missing/.index", "/assets/cores/" + url.PathEscape(platform) + "/missing.zip", "/assets/cores/" + url.PathEscape(platform) + "/game.m3u", "/assets/cores/" + url.PathEscape(platform) + "/%2e%2e/catalog.json"} {
		if got := request(t, h, http.MethodGet, target).Code; got != http.StatusNotFound {
			t.Errorf("GET %s: %d, want 404", target, got)
		}
	}
	api := request(t, h, http.MethodGet, "/api/games")
	var gotCards []map[string]interface{}
	if api.Code != http.StatusOK || json.Unmarshal(api.Body.Bytes(), &gotCards) != nil || len(gotCards) != len(cards) {
		t.Fatalf("existing API: %d %s", api.Code, api.Body.String())
	}
	for i, id := range ids {
		if gotCards[i]["id"] != id || gotCards[i]["download_url"] != "packages/"+url.PathEscape(id)+".zip" {
			t.Errorf("existing API card: %v", gotCards[i])
		}
	}
}

func TestAssetEmptyCatalog(t *testing.T) {
	catalog, packages := fixture(t, `[]`, nil)
	h, err := New(catalog, packages)
	if err != nil {
		t.Fatal(err)
	}
	if got := assetIndex(t, h, "/assets/cores/.index-dirs"); len(got) != 0 {
		t.Fatalf("empty catalog categories: %q", got)
	}
	if got := request(t, h, http.MethodGet, "/assets/cores/missing/.index").Code; got != http.StatusNotFound {
		t.Fatalf("missing category: %d", got)
	}
}

func TestAssetCaseCollisions(t *testing.T) {
	ids := []string{"Game", "game", "third"}
	platforms := []string{"System", "System", "system"}
	var cards []map[string]interface{}
	packages := make(map[string][]zipEntry)
	for i, id := range ids {
		cards = append(cards, map[string]interface{}{
			"id": id, "canonical_name": id,
			"platform":   map[string]string{"id": "p", "name": "Platform", "libretro_name": platforms[i]},
			"entrypoint": "file.txt", "files": []string{"file.txt"}, "download_url": "original.zip",
		})
		packages[id] = []zipEntry{{name: "file.txt", data: id}}
	}
	manifest, err := json.Marshal(cards)
	if err != nil {
		t.Fatal(err)
	}
	catalog, dir := fixture(t, string(manifest), packages)
	h, err := New(catalog, dir)
	if err != nil {
		t.Fatal(err)
	}
	wantCategories := []string{assetLabel("System"), assetLabel("system")}
	if got := assetIndex(t, h, "/assets/cores/.index-dirs"); !reflect.DeepEqual(got, wantCategories) {
		t.Fatalf("case-colliding categories %q, want %q", got, wantCategories)
	}
	base := "/assets/cores/" + url.PathEscape(wantCategories[0]) + "/"
	wantGames := []string{assetLabel("Game") + "-47616d65.zip", assetLabel("game") + "-67616d65.zip"}
	if got := assetIndex(t, h, base+".index"); !reflect.DeepEqual(got, wantGames) {
		t.Fatalf("case-colliding games %q, want %q", got, wantGames)
	}
	for _, name := range wantGames {
		if got := request(t, h, http.MethodGet, base+url.PathEscape(name)).Code; got != http.StatusOK {
			t.Errorf("download %q: %d", name, got)
		}
	}
}

func TestAssetCollisionRoutesAndHTTP(t *testing.T) {
	games := []game{
		{id: "one", canonical: "Same", platform: "System"},
		{id: "two", canonical: "Same", platform: "System"},
		{id: "foo", canonical: "Other", platform: "System"},
		{id: "FOO", canonical: "Another", platform: "System"},
		{id: "third", canonical: "foo", platform: "System"},
		{id: "fourth", canonical: "~53616d65-6f6e65", platform: "System"},
	}
	routes := assetRoutes(games)
	base := "/assets/cores/System/"
	index := strings.Split(strings.TrimSpace(routes[base+".index"].index), "\n")
	if len(index) != len(games) {
		t.Fatalf("index: %q", index)
	}
	seen := map[string]bool{}
	for _, name := range index {
		if seen[strings.ToLower(name)] {
			t.Fatalf("duplicate index name %q", name)
		}
		seen[strings.ToLower(name)] = true
	}
	if routes[base+"foo.zip"].archive != "third" || routes[base+"Other.zip"].archive != "foo" || routes[base+"Another.zip"].archive != "FOO" {
		t.Fatalf("primary routes overwritten: %v", routes)
	}
	if _, ok := routes[base+"FOO.zip"]; ok {
		t.Fatal("ambiguous alias published")
	}
	for i, j := 0, len(games)-1; i < j; i, j = i+1, j-1 {
		games[i], games[j] = games[j], games[i]
	}
	if reversed := assetRoutes(games); !reflect.DeepEqual(routes, reversed) {
		t.Fatal("routes depend on input order")
	}

	var cards []map[string]interface{}
	packages := make(map[string][]zipEntry)
	for _, g := range games[:2] {
		cards = append(cards, map[string]interface{}{
			"id": g.id, "canonical_name": "Duplicate",
			"platform":   map[string]string{"id": "p", "name": "Platform", "libretro_name": "System"},
			"entrypoint": "file.txt", "files": []string{"file.txt"}, "download_url": "original.zip",
		})
		packages[g.id] = []zipEntry{{name: "file.txt", data: g.id}}
	}
	manifest, err := json.Marshal(cards)
	if err != nil {
		t.Fatal(err)
	}
	source, dir := fixture(t, string(manifest), packages)
	handler, err := New(source, dir)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	resp, err := http.Get(server.URL + base + ".index")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("index: %s %v", body, err)
	}
	entries := strings.Split(strings.TrimSpace(string(body)), "\n")
	if len(entries) != 2 || strings.EqualFold(entries[0], entries[1]) {
		t.Fatalf("index %q", entries)
	}
	found := map[string]bool{}
	for _, entry := range entries {
		resp, err := http.Get(server.URL + base + url.PathEscape(entry))
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("zip: %v %s", err, resp.Status)
		}
		zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil || len(zr.File) != 1 {
			t.Fatalf("zip: %v", err)
		}
		if zr.File[0].Name != strings.TrimSuffix(entry, ".zip")+"/file.txt" {
			t.Fatal(zr.File[0].Name)
		}
		r, err := zr.File[0].Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		found[string(content)] = true
	}
	for _, g := range games[:2] {
		if !found[g.id] {
			t.Fatalf("missing content %q", g.id)
		}
	}
}

func TestAssetUsesCanonicalName(t *testing.T) {
	manifest, packages := fixture(t, `[{
		"id": "ct-snes",
		"canonical_name": "Chrono Trigger (USA)",
		"platform": {"id": "snes", "name": "Super Nintendo", "libretro_name": "Nintendo - Super Nintendo Entertainment System"},
		"entrypoint": "game.sfc",
		"files": ["game.sfc"],
		"download_url": "dummy.zip"
	}]`, map[string][]zipEntry{
		"ct-snes": {{name: "game.sfc", data: "rom data"}},
	})
	h, err := New(manifest, packages)
	if err != nil {
		t.Fatal(err)
	}
	base := "/assets/cores/" + url.PathEscape("Nintendo - Super Nintendo Entertainment System")
	index := assetIndex(t, h, base+"/.index")
	want := []string{"Chrono Trigger (USA).zip"}
	if !reflect.DeepEqual(index, want) {
		t.Fatalf("index: %q, want %q", index, want)
	}
	target := base + "/" + url.PathEscape("Chrono Trigger (USA).zip")
	w := request(t, h, http.MethodGet, target)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s: %d", target, w.Code)
	}
}

func TestAssetAssetsZip(t *testing.T) {
	manifest, packages := fixture(t, "[]", nil)
	h, err := New(manifest, packages)
	if err != nil {
		t.Fatal(err)
	}

	origURL := UpstreamAssetsURL
	defer func() { UpstreamAssetsURL = origURL }()
	UpstreamAssetsURL = ""

	for _, target := range []string{"/assets/frontend/assets.zip", "/assets/assets.zip"} {
		w := request(t, h, http.MethodGet, target)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: %d", target, w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/zip") {
			t.Fatalf("GET %s: wrong Content-Type %s", target, ct)
		}
		zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
		if err != nil {
			t.Fatalf("GET %s: invalid zip: %v", target, err)
		}
		if len(zr.File) == 0 {
			t.Fatalf("GET %s: empty zip", target)
		}
	}
}

func TestAssetsFetchLimitsAndRetry(t *testing.T) {
	body := "12345"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte(body))
	}))
	defer upstream.Close()
	old := UpstreamAssetsURL
	UpstreamAssetsURL = upstream.URL
	defer func() { UpstreamAssetsURL = old }()
	_, packages := fixture(t, "[]", nil)
	s := &Server{packagesDir: packages}
	if err := s.fetchAssets(context.Background(), 4); err == nil {
		t.Fatal("accepted oversized response")
	}
	if _, err := os.Stat(filepath.Join(packages, ".cache", "assets.zip")); !os.IsNotExist(err) {
		t.Fatalf("partial cache: %v", err)
	}
	body = "1234"
	if err := s.fetchAssets(context.Background(), 4); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(packages, ".cache", "assets.zip"))
	if err != nil || string(data) != body {
		t.Fatalf("cache %q: %v", data, err)
	}
}

func TestAssetsSharedHTTP(t *testing.T) {
	var bundle bytes.Buffer
	zw := zip.NewWriter(&bundle)
	member, err := zw.Create("asset.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := member.Write([]byte("asset")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
		}
		select {
		case <-release:
			_, _ = w.Write(bundle.Bytes())
		case <-r.Context().Done():
		}
	}))
	defer upstream.Close()
	old := UpstreamAssetsURL
	UpstreamAssetsURL = upstream.URL
	defer func() { UpstreamAssetsURL = old }()
	manifest, packages := fixture(t, "[]", nil)
	s, err := New(manifest, packages)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(s)
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/assets/assets.zip", nil)
	first := make(chan error, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		first <- err
	}()
	<-started
	second := make(chan []byte, 1)
	go func() {
		resp, err := http.Get(server.URL + "/assets/frontend/assets.zip")
		if err != nil {
			second <- nil
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		second <- b
	}()
	third := make(chan []byte, 1)
	go func() {
		resp, err := http.Get(server.URL + "/assets/assets.zip")
		if err != nil {
			third <- nil
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		third <- b
	}()
	deadline := time.After(5 * time.Second)
	for {
		s.assetsMu.Lock()
		n := s.assetsFetch.waiters
		s.assetsMu.Unlock()
		if n == 3 {
			break
		}
		select {
		case <-deadline:
			close(release)
			t.Fatal("second waiter did not join")
		default:
			runtime.Gosched()
		}
	}
	cancel()
	if err := <-first; err == nil {
		t.Fatal("cancelled request succeeded")
	}
	close(release)
	if got := <-second; !bytes.Equal(got, bundle.Bytes()) {
		t.Fatalf("bundle %q", got)
	}
	if got := <-third; !bytes.Equal(got, bundle.Bytes()) {
		t.Fatalf("second bundle %q", got)
	}
	resp, err := http.Get(server.URL + "/assets/assets.zip")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil || !bytes.Equal(data, bundle.Bytes()) || calls.Load() != 1 {
		t.Fatalf("cache %q calls %d: %v", data, calls.Load(), err)
	}
}

func TestAssetsAllCancelledThenRetry(t *testing.T) {
	started, cancelled := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
			<-r.Context().Done()
			close(cancelled)
			return
		}
		_, _ = w.Write([]byte("retry"))
	}))
	defer upstream.Close()
	old := UpstreamAssetsURL
	UpstreamAssetsURL = upstream.URL
	defer func() { UpstreamAssetsURL = old }()
	_, packages := fixture(t, "[]", nil)
	s := &Server{packagesDir: packages}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.waitAssets(ctx); close(done) }()
	<-started
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := make(chan struct{})
	go func() { s.waitAssets(ctx2); close(done2) }()
	deadline := time.After(5 * time.Second)
	for {
		s.assetsMu.Lock()
		n := s.assetsFetch.waiters
		s.assetsMu.Unlock()
		if n == 2 {
			break
		}
		select {
		case <-deadline:
			cancel()
			cancel2()
			t.Fatal("second waiter did not join")
		default:
			runtime.Gosched()
		}
	}
	cancel()
	<-done
	cancel2()
	<-done2
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("upstream not cancelled")
	}
	s.waitAssets(context.Background())
	data, err := os.ReadFile(filepath.Join(packages, ".cache", "assets.zip"))
	if err != nil || string(data) != "retry" || calls.Load() != 2 {
		t.Fatalf("retry %q calls %d: %v", data, calls.Load(), err)
	}
}

func TestAssetsFetchFailures(t *testing.T) {
	for _, mode := range []string{"status", "truncated", "publication"} {
		t.Run(mode, func(t *testing.T) {
			var retry atomic.Bool
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if retry.Load() {
					_, _ = w.Write([]byte("retry"))
					return
				}
				switch mode {
				case "status":
					w.WriteHeader(http.StatusBadGateway)
				case "truncated":
					w.Header().Set("Content-Length", "20")
					_, _ = w.Write([]byte("short"))
				default:
					_, _ = w.Write([]byte("valid"))
				}
			}))
			defer upstream.Close()
			old := UpstreamAssetsURL
			UpstreamAssetsURL = upstream.URL
			defer func() { UpstreamAssetsURL = old }()
			_, packages := fixture(t, "[]", nil)
			s := &Server{packagesDir: packages}
			dest := filepath.Join(packages, ".cache", "assets.zip")
			if mode == "publication" {
				if err := os.MkdirAll(dest, 0755); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.fetchAssets(context.Background(), 100); err == nil {
				t.Fatal("expected failure")
			}
			if mode == "publication" {
				if err := os.Remove(dest); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := os.Stat(dest); !os.IsNotExist(err) {
					t.Fatalf("partial file: %v", err)
				}
			}
			retry.Store(true)
			if err := s.fetchAssets(context.Background(), 100); err != nil {
				t.Fatalf("retry: %v", err)
			}
		})
	}
}
