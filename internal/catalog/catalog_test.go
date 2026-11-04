package catalog

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const card = `{"id":"example","canonical_name":"Étoile Game","display_name":"Приключение","platform":{"id":"psx","name":"PlayStation","libretro_name":"Sony - PlayStation"},"entrypoint":"game.txt","files":["game.txt"],"download_url":"original.zip"}`

type zipEntry struct {
	name string
	data string
	mode os.FileMode
}

// fixture translates card-shaped test data into the domain source format.
func fixture(t *testing.T, manifest string, packages map[string][]zipEntry) (string, string) {
	t.Helper()
	dir := t.TempDir()
	var cards []struct {
		ID        string  `json:"id"`
		Canonical string  `json:"canonical_name"`
		Display   *string `json:"display_name"`
		Platform  struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			LibretroName string `json:"libretro_name"`
		} `json:"platform"`
		Entrypoint string `json:"entrypoint"`
	}
	if err := json.Unmarshal([]byte(manifest), &cards); err != nil {
		t.Fatal(err)
	}
	platforms := []any{}
	contents := []any{}
	seen := map[string]string{}
	for i, c := range cards {
		platformID, ok := seen[c.Platform.LibretroName]
		if !ok {
			platformID = fmt.Sprintf("platform-%d", len(seen))
			c.Platform.ID = platformID
			platforms = append(platforms, c.Platform)
			seen[c.Platform.LibretroName] = platformID
		}
		files := []any{}
		for j, entry := range packages[c.ID] {
			name := fmt.Sprintf("source-%d-%d", i, j)
			if err := os.WriteFile(filepath.Join(dir, name), []byte(entry.data), 0600); err != nil {
				t.Fatal(err)
			}
			files = append(files, map[string]string{"source": name, "path": entry.name})
		}
		content := map[string]any{"id": c.ID, "canonical_name": c.Canonical, "platform_id": platformID,
			"discs": []any{map[string]any{"entrypoint": c.Entrypoint, "files": files}}}
		if c.Display != nil {
			content["display_name"] = *c.Display
		}
		contents = append(contents, content)
	}
	data, err := json.Marshal(map[string]any{"platforms": platforms, "contents": contents})
	if err != nil {
		t.Fatal(err)
	}
	catalog := filepath.Join(dir, "catalog-source.json")
	if err := os.WriteFile(catalog, data, 0600); err != nil {
		t.Fatal(err)
	}
	return catalog, filepath.Join(dir, "packages")
}

func request(t *testing.T, h http.Handler, method, target string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, target, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func cacheEntries(t *testing.T, packages string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(packages, ".cache"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestCatalogAndSearch(t *testing.T) {
	second := strings.Replace(card, `"example"`, `"second"`, 1)
	second = strings.Replace(second, `Étoile Game`, `Another Game`, 1)
	second = strings.Replace(second, `Приключение`, `Other title`, 1)
	catalog, packages := fixture(t, "["+card+","+second+"]", map[string][]zipEntry{
		"example": {{name: "game.txt", data: "content"}}, "second": {{name: "game.txt", data: "other"}},
	})
	h, err := New(catalog, packages)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query string
		ids   []string
	}{
		{"", []string{"example", "second"}}, {"?q=", []string{"example", "second"}},
		{"?q=" + url.QueryEscape("éTOILE"), []string{"example"}},
		{"?q=" + url.QueryEscape("ПРИКЛЮЧЕНИЕ"), []string{"example"}},
		{"?q=game", []string{"example", "second"}}, {"?q=notfound", []string{}},
		{"?q=game&q=notfound", []string{"example", "second"}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			w := request(t, h, http.MethodGet, "/api/games"+tc.query)
			if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
				t.Fatalf("response: %d %v", w.Code, w.Header())
			}
			var cards []struct {
				ID          string `json:"id"`
				DownloadURL string `json:"download_url"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &cards); err != nil || cards == nil || len(cards) != len(tc.ids) {
				t.Fatalf("cards: %s (%v)", w.Body.String(), err)
			}
			for i, id := range tc.ids {
				if cards[i].ID != id || cards[i].DownloadURL != "packages/"+id+".zip" {
					t.Fatalf("card: %+v", cards[i])
				}
			}
		})
	}
	for _, target := range []string{"/", "/api/packages/missing.zip", "/api/other"} {
		if got := request(t, h, "GET", target).Code; got != 404 {
			t.Errorf("GET %s: %d", target, got)
		}
	}
	for _, target := range []string{"/api/games", "/api/packages/example.zip"} {
		for _, method := range []string{"POST", "HEAD"} {
			w := request(t, h, method, target)
			if w.Code != 405 || w.Header().Get("Allow") != "GET" {
				t.Errorf("%s %s: %d Allow=%q", method, target, w.Code, w.Header().Get("Allow"))
			}
		}
	}
	if got := request(t, h, "GET", "/api/games?q=%zz").Code; got != 400 {
		t.Errorf("malformed query: %d", got)
	}
	if entries := cacheEntries(t, packages); len(entries) != 0 {
		t.Fatalf("unexpected eager cache: %v", entries)
	}
	w := request(t, h, "GET", "/api/packages/example.zip")
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("package: %d %v", w.Code, w.Header())
	}
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil || len(zr.File) != 1 || zr.File[0].Name != "game.txt" {
		t.Fatalf("ZIP: %v", err)
	}
	r, err := zr.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(r)
	r.Close()
	if err != nil || string(data) != "content" {
		t.Fatalf("ZIP content: %q %v", data, err)
	}
}

func TestEmptyCatalogAndOptionalFields(t *testing.T) {
	catalog, packages := fixture(t, `[]`, nil)
	h, err := New(catalog, packages)
	if err != nil {
		t.Fatal(err)
	}
	w := request(t, h, "GET", "/api/games")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("empty: %d %q", w.Code, w.Body.String())
	}
	minimal := strings.Replace(card, `,"display_name":"Приключение"`, "", 1)
	catalog, packages = fixture(t, "["+minimal+"]", map[string][]zipEntry{"example": {{name: "game.txt", data: "x"}}})
	h, err = New(catalog, packages)
	if err != nil {
		t.Fatal(err)
	}
	w = request(t, h, "GET", "/api/games")
	var cards []map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &cards); err != nil || len(cards) != 1 {
		t.Fatalf("cards: %s (%v)", w.Body.String(), err)
	}
	if _, ok := cards[0]["display_name"]; ok {
		t.Fatal("unexpected display_name")
	}
}

func TestParseGameValidation(t *testing.T) {
	replace := func(old, new string) string { return strings.Replace(card, old, new, 1) }
	cases := map[string]string{
		"duplicate field":    replace(`"id":"example"`, `"id":"example","id":"example"`),
		"duplicate platform": replace(`"name":"PlayStation"`, `"name":"PlayStation","name":"PlayStation"`),
		"duplicate file":     replace(`["game.txt"]`, `["game.txt","game.txt"]`),
		"missing entrypoint": replace(`"entrypoint":"game.txt"`, `"entrypoint":"missing.txt"`),
		"missing name":       replace(`"canonical_name":"Étoile Game",`, ``),
		"wrong type":         replace(`"canonical_name":"Étoile Game"`, `"canonical_name":1`),
		"empty optional":     replace(`"display_name":"Приключение"`, `"display_name":""`),
		"nul string":         replace(`"canonical_name":"Étoile Game"`, `"canonical_name":"a\u0000b"`),
		"unsafe id":          replace(`"id":"example"`, `"id":"../example"`),
		"unsafe platform":    replace(`"libretro_name":"Sony - PlayStation"`, `"libretro_name":"foo/bar"`),
	}
	for _, path := range []string{"/absolute", "../file", "a/../file", "a//file", "./file", `a\b`, "a:b", "a\u0001b"} {
		encoded, _ := json.Marshal(path)
		cases["unsafe path "+path] = strings.ReplaceAll(card, `"game.txt"`, string(encoded))
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseGame(json.RawMessage(raw)); err == nil {
				t.Fatal("accepted invalid card")
			}
		})
	}
}

func TestLazyCacheAndReload(t *testing.T) {
	catalog, packages := fixture(t, "["+card+"]", map[string][]zipEntry{"example": {{name: "game.txt", data: "original"}}})
	if err := os.Mkdir(packages, 0700); err != nil {
		t.Fatal(err)
	}
	userZIP := filepath.Join(packages, "example.zip")
	if err := os.WriteFile(userZIP, []byte("user data"), 0600); err != nil {
		t.Fatal(err)
	}
	h, err := New(catalog, packages)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	codes := make(chan int, 12)
	for range cap(codes) {
		wg.Add(1)
		go func() { defer wg.Done(); codes <- request(t, h, "GET", "/api/packages/example.zip").Code }()
	}
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != 200 {
			t.Fatalf("concurrent download: %d", code)
		}
	}
	entries := cacheEntries(t, packages)
	if len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".zip") {
		t.Fatalf("cache entries: %v", entries)
	}
	info, err := entries[0].Info()
	if err != nil {
		t.Fatal(err)
	}
	h2, err := New(catalog, packages)
	if err != nil {
		t.Fatal(err)
	}
	if got := request(t, h2, "GET", "/api/packages/example.zip").Code; got != 200 {
		t.Fatalf("restart download: %d", got)
	}
	after, err := os.Stat(filepath.Join(packages, ".cache", entries[0].Name()))
	if err != nil || !after.ModTime().Equal(info.ModTime()) {
		t.Fatalf("cache not reused: %v", err)
	}
	data, err := os.ReadFile(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalog, []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"/api/games", "/api/packages/example.zip", "/missing"} {
		if got := request(t, h, "GET", target).Code; got != 500 {
			t.Errorf("invalid manifest %s: %d", target, got)
		}
	}
	changed := strings.Replace(string(data), "Étoile Game", "Changed Game", 1)
	if err := os.WriteFile(catalog, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	w := request(t, h, "GET", "/api/games?q=changed")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Changed Game") {
		t.Fatalf("reload: %d %s", w.Code, w.Body.String())
	}
	if got := request(t, h, "GET", "/api/packages/example.zip").Code; got != 200 {
		t.Fatalf("new download: %d", got)
	}
	if entries := cacheEntries(t, packages); len(entries) != 2 {
		t.Fatalf("new snapshot cache: %v", entries)
	}
	if data, err := os.ReadFile(userZIP); err != nil || string(data) != "user data" {
		t.Fatalf("user ZIP changed: %q %v", data, err)
	}
}

func TestEncodedIDAndCacheSymlink(t *testing.T) {
	id := "game #1"
	manifest := strings.Replace(card, `"id":"example"`, fmt.Sprintf(`"id":%q`, id), 1)
	catalog, packages := fixture(t, "["+manifest+"]", map[string][]zipEntry{id: {{name: "game.txt", data: "x"}}})
	h, err := New(catalog, packages)
	if err != nil {
		t.Fatal(err)
	}
	w := request(t, h, "GET", "/api/games")
	var cards []struct {
		DownloadURL string `json:"download_url"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &cards); err != nil || len(cards) != 1 {
		t.Fatalf("catalog: %s (%v)", w.Body.String(), err)
	}
	base, _ := url.Parse("http://example.com/api/games")
	ref, err := url.Parse(cards[0].DownloadURL)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(packages, 0700); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(packages, ".cache")); err != nil {
		t.Fatal(err)
	}
	if got := request(t, h, "GET", base.ResolveReference(ref).String()).Code; got != 500 {
		t.Fatalf("symlink cache: %d", got)
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("wrote outside cache: %v %v", entries, err)
	}
}

func TestCacheTracksSourcesAndManifestPath(t *testing.T) {
	catalog, packages := fixture(t, "["+card+"]", map[string][]zipEntry{"example": {{name: "game.txt", data: "first"}}})
	h, err := New(catalog, packages)
	if err != nil {
		t.Fatal(err)
	}
	if got := request(t, h, "GET", "/api/packages/example.zip").Code; got != 200 {
		t.Fatal(got)
	}
	source := filepath.Join(filepath.Dir(catalog), "source-0-0")
	if err := os.WriteFile(source, []byte("different content"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := request(t, h, "GET", "/api/packages/example.zip").Code; got != 200 {
		t.Fatal(got)
	}
	if entries := cacheEntries(t, packages); len(entries) != 2 {
		t.Fatalf("source change cache: %v", entries)
	}
	data, err := os.ReadFile(catalog)
	if err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(filepath.Dir(catalog), "other.json")
	if err := os.WriteFile(other, data, 0600); err != nil {
		t.Fatal(err)
	}
	h2, err := New(other, packages)
	if err != nil {
		t.Fatal(err)
	}
	if got := request(t, h2, "GET", "/api/packages/example.zip").Code; got != 200 {
		t.Fatal(got)
	}
	if entries := cacheEntries(t, packages); len(entries) != 3 {
		t.Fatalf("manifest path cache: %v", entries)
	}
	changed := strings.Replace(string(data), `"id":"example"`, `"id":"renamed"`, 1)
	if err := os.WriteFile(catalog, []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	if got := request(t, h, "GET", "/api/packages/example.zip").Code; got != 404 {
		t.Fatalf("old route: %d", got)
	}
	if got := request(t, h, "GET", "/api/packages/renamed.zip").Code; got != 200 {
		t.Fatalf("new route: %d", got)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if got := request(t, h, "GET", "/api/games").Code; got != 500 {
		t.Fatalf("missing source: %d", got)
	}
	if _, err := New(catalog, packages); err == nil {
		t.Fatal("startup accepted missing source")
	}
}

func TestDeferredGameAvailability(t *testing.T) {
	second := strings.Replace(card, `"example"`, `"second"`, 1)
	catalogPath, packages := fixture(t, "["+card+","+second+"]", map[string][]zipEntry{
		"example": {{name: "game.txt", data: "content"}},
		"second":  {{name: "game.txt", data: "other"}},
	})
	data, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["contents"].([]any)[0].(map[string]any)["defer_missing"] = true
	data, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(catalogPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(filepath.Dir(catalogPath), "source-0-0")
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	for restart := range 2 {
		h, err := New(catalogPath, packages)
		if err != nil {
			t.Fatal(err)
		}
		w := request(t, h, http.MethodGet, "/api/games")
		if w.Code != http.StatusOK || strings.Contains(w.Body.String(), `"id":"example"`) || !strings.Contains(w.Body.String(), `"id":"second"`) {
			t.Fatalf("pending catalog: %d %s", w.Code, w.Body.String())
		}
		for _, target := range []string{"/api/packages/example.zip", "/assets/cores/Sony%20-%20PlayStation/example.zip"} {
			if w := request(t, h, http.MethodGet, target); w.Code != http.StatusNotFound {
				t.Fatalf("pending %s: %d", target, w.Code)
			}
		}
		if restart == 1 {
			if err := os.WriteFile(source, []byte("content"), 0600); err != nil {
				t.Fatal(err)
			}
			w = request(t, h, http.MethodGet, "/api/games")
			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"id":"example"`) {
				t.Fatalf("activated catalog: %d %s", w.Code, w.Body.String())
			}
			for _, target := range []string{"/api/packages/example.zip", "/assets/cores/Sony%20-%20PlayStation/example.zip"} {
				if w := request(t, h, http.MethodGet, target); w.Code != http.StatusOK {
					t.Fatalf("activated %s: %d", target, w.Code)
				}
			}
		}
	}
}
