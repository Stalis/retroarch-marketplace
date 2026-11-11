package catalog

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"retroarch-marketplace/internal/store"
)

func adminRequest(h http.Handler, method, target string, body []byte, etag string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://localhost:5050"+target, bytes.NewReader(body))
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("Origin", "http://localhost:5050")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("If-Match", etag)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

const adminGame = `{"id":"g","canonical_name":"G","platform_id":"p","discs":[{"entrypoint":"g.bin","files":[{"source":"g.bin","path":"g.bin"}]}]}`

func adminFixture(t *testing.T) (*store.Store, *Server) {
	t.Helper()
	st, packages := fixture(t, "[]", nil)
	h, err := New(st, packages)
	if err != nil {
		t.Fatal(err)
	}
	if w := adminRequest(h, "POST", "/api/admin/platforms", []byte(`{"id":"p","name":"P","libretro_name":"P"}`), ""); w.Code != 201 {
		t.Fatalf("platform: %d %s", w.Code, w.Body.String())
	}
	return st, h
}

func TestAdminGameCRUD(t *testing.T) {
	st, h := adminFixture(t)
	for _, tc := range []struct {
		method, target, body string
		status               int
	}{
		{"POST", "/api/admin/platforms", `{"id":"p","name":"P","libretro_name":"P"}`, 409},
		{"POST", "/api/admin/platforms", `{"id":"a/b","name":"P","libretro_name":"P"}`, 400},
		{"POST", "/api/admin/platforms", `{"id":"q","name":"Q","libretro_name":"Q","extra":1}`, 400},
		{"POST", "/api/admin/games", strings.Replace(adminGame, `"p"`, `"nope"`, 1), 400},
		{"POST", "/api/admin/games", strings.Replace(adminGame, `"source":"g.bin"`, `"source":"../g.bin"`, 1), 400},
		{"POST", "/api/admin/games", adminGame, 422},
		{"GET", "/api/admin/games/g", "", 404},
		{"PATCH", "/api/admin/games/g", "", 405},
		{"GET", "/api/admin/games/..", "", 404},
		{"GET", "/api/admin/unknown", "", 404},
	} {
		if w := adminRequest(h, tc.method, tc.target, []byte(tc.body), ""); w.Code != tc.status {
			t.Errorf("%s %s: %d %s", tc.method, tc.target, w.Code, w.Body.String())
		}
	}
	w := adminRequest(h, "POST", "/api/admin/games?allow_missing=1", []byte(adminGame), "")
	if w.Code != 201 || w.Header().Get("ETag") != `"1"` || w.Header().Get("Location") != "/api/admin/games/g" {
		t.Fatalf("create: %d %v %s", w.Code, w.Header(), w.Body.String())
	}
	if w := adminRequest(h, "POST", "/api/admin/games?allow_missing=1", []byte(adminGame), ""); w.Code != 409 {
		t.Fatalf("duplicate: %d", w.Code)
	}
	w = adminRequest(h, "GET", "/api/admin/games/g", nil, "")
	if w.Code != 200 || w.Header().Get("ETag") != `"1"` || !strings.Contains(w.Body.String(), `"available":false`) || !strings.Contains(w.Body.String(), `"path":"g.bin"`) {
		t.Fatalf("get: %d %s", w.Code, w.Body.String())
	}
	if w := adminRequest(h, "GET", "/api/admin/platforms", nil, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"games":0`) || !strings.Contains(w.Body.String(), `"total_games":1`) {
		t.Fatalf("platforms: %s", w.Body.String())
	}
	if w := adminRequest(h, "GET", "/api/admin/games?q=G", nil, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"id":"g"`) {
		t.Fatalf("list: %s", w.Body.String())
	}
	if w := request(t, h, "GET", "/api/games"); strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("unavailable game listed: %s", w.Body.String())
	}
	if err := os.WriteFile(filepath.Join(st.Root(), "g.bin"), []byte("rom"), 0600); err != nil {
		t.Fatal(err)
	}
	renamed := []byte(strings.Replace(adminGame, `"G"`, `"Renamed"`, 1))
	for _, tc := range []struct {
		target, etag, body string
		status             int
	}{
		{"/api/admin/games/g", "", string(renamed), 412},
		{"/api/admin/games/g", `"7"`, string(renamed), 412},
		{"/api/admin/games/h", `"1"`, string(renamed), 400},
		{"/api/admin/games/h", `"1"`, strings.Replace(string(renamed), `"g"`, `"h"`, 1), 404},
	} {
		if w := adminRequest(h, "PUT", tc.target, []byte(tc.body), tc.etag); w.Code != tc.status {
			t.Errorf("PUT %s %s: %d %s", tc.target, tc.etag, w.Code, w.Body.String())
		}
	}
	w = adminRequest(h, "PUT", "/api/admin/games/g", renamed, `"1"`)
	if w.Code != 204 || w.Header().Get("ETag") != `"2"` {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	if w := request(t, h, "GET", "/api/games"); !strings.Contains(w.Body.String(), "Renamed") {
		t.Fatalf("update not public: %s", w.Body.String())
	}
	if w := adminRequest(h, "DELETE", "/api/admin/platforms/p", nil, ""); w.Code != 409 {
		t.Fatalf("delete used platform: %d", w.Code)
	}
	if w := adminRequest(h, "DELETE", "/api/admin/games/g", nil, `"1"`); w.Code != 412 {
		t.Fatalf("stale delete: %d", w.Code)
	}
	if w := adminRequest(h, "DELETE", "/api/admin/games/g", nil, `"2"`); w.Code != 204 {
		t.Fatalf("delete: %d", w.Code)
	}
	if w := adminRequest(h, "DELETE", "/api/admin/games/g", nil, `"2"`); w.Code != 404 {
		t.Fatalf("delete absent: %d", w.Code)
	}
	for target, want := range map[string]int{"/api/admin/platforms/p": 204, "/api/admin/platforms/p ": 404} {
		if w := adminRequest(h, "DELETE", strings.ReplaceAll(target, " ", "%20"), nil, ""); w.Code != want {
			t.Errorf("DELETE %s: %d", target, w.Code)
		}
	}
	if w := adminRequest(h, "GET", "/api/admin/known-platforms", nil, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "Sony - PlayStation") {
		t.Fatalf("known platforms: %d", w.Code)
	}
	if source, err := os.ReadFile(filepath.Join(st.Root(), "g.bin")); err != nil || string(source) != "rom" {
		t.Fatalf("source changed: %q %v", source, err)
	}
}

func TestAdminSecurity(t *testing.T) {
	_, h := adminFixture(t)
	for _, tc := range []struct {
		name, remote, host, origin, contentType, method, target string
		status                                                  int
	}{
		{"remote", "192.0.2.1:42", "localhost:5050", "http://localhost:5050", "application/json", "GET", "/api/admin/games", 403},
		{"host", "127.0.0.1:42", "evil.test:5050", "http://evil.test:5050", "application/json", "GET", "/api/admin/games", 403},
		{"origin missing", "127.0.0.1:42", "localhost:5050", "", "application/json", "POST", "/api/admin/games", 403},
		{"origin cross post", "127.0.0.1:42", "localhost:5050", "http://evil.test", "application/json", "POST", "/api/admin/games?allow_missing=1", 403},
		{"origin cross put", "127.0.0.1:42", "localhost:5050", "http://evil.test", "application/json", "PUT", "/api/admin/games/g", 403},
		{"origin cross delete", "127.0.0.1:42", "localhost:5050", "http://evil.test", "", "DELETE", "/api/admin/platforms/p", 403},
		{"origin scheme", "127.0.0.1:42", "localhost:5050", "https://localhost:5050", "application/json", "POST", "/api/admin/platforms", 403},
		{"content type", "127.0.0.1:42", "localhost:5050", "http://localhost:5050", "text/plain", "POST", "/api/admin/games", 400},
		{"static remote", "192.0.2.1:42", "localhost:5050", "", "", "GET", "/admin/", 403},
		{"static host", "127.0.0.1:42", "evil.test", "", "", "GET", "/admin/", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://localhost:5050"+tc.target, strings.NewReader(adminGame))
			r.RemoteAddr, r.Host = tc.remote, tc.host
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Content-Type", tc.contentType)
			r.Header.Set("If-Match", `"1"`)
			r.Header.Set("X-Forwarded-For", "127.0.0.1")
			r.Header.Set("X-Forwarded-Host", "localhost:5050")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
		})
	}
	if w := adminRequest(h, "POST", "/api/admin/games", bytes.Repeat([]byte(" "), maxAdminBody+1), ""); w.Code != 400 {
		t.Fatalf("oversized: %d", w.Code)
	}
	if w := adminRequest(h, "GET", "/api/admin/games", nil, ""); w.Header().Get("X-Catalog-Version") != Version || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("nothing stored: %v %s", w.Header(), w.Body.String())
	}
}

func TestAdminConcurrentUpdates(t *testing.T) {
	st, h := adminFixture(t)
	if err := os.WriteFile(filepath.Join(st.Root(), "g.bin"), []byte("rom"), 0600); err != nil {
		t.Fatal(err)
	}
	if w := adminRequest(h, "POST", "/api/admin/games", []byte(adminGame), ""); w.Code != 201 {
		t.Fatal(w.Code)
	}
	results := make(chan int, 2)
	for _, name := range []string{"A", "B"} {
		go func() {
			results <- adminRequest(h, "PUT", "/api/admin/games/g", []byte(strings.Replace(adminGame, `"G"`, `"`+name+`"`, 1)), `"1"`).Code
		}()
	}
	first, second := <-results, <-results
	if !((first == 204 && second == 412) || (first == 412 && second == 204)) {
		t.Fatalf("concurrent statuses: %d, %d", first, second)
	}
}

func TestAdminAllowsSymlinkedSources(t *testing.T) {
	st, h := adminFixture(t)
	real := filepath.Join(st.Root(), "real.bin")
	if err := os.WriteFile(real, []byte("rom"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(st.Root(), "g.bin")); err != nil {
		t.Fatal(err)
	}
	if w := adminRequest(h, "POST", "/api/admin/games", []byte(adminGame), ""); w.Code != 201 {
		t.Fatalf("symlink source: %d %s", w.Code, w.Body.String())
	}
}

func TestAdminStatic(t *testing.T) {
	st, packages := fixture(t, "[]", nil)
	h, err := New(st, packages)
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)
	w := adminRequest(h, "GET", "/admin", nil, "")
	if w.Code != 301 || w.Header().Get("Location") != "/admin/" {
		t.Fatalf("redirect: %d", w.Code)
	}
	w = adminRequest(h, "GET", "/admin/", nil, "")
	if w.Code != 503 || !strings.Contains(w.Body.String(), "build") {
		t.Fatalf("missing build: %d %s", w.Code, w.Body.String())
	}
	if err := os.MkdirAll("web/dist/assets", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("web/dist/index.html", []byte("admin app"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("web/dist/assets/app.js", []byte("app"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("web/secret", []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../secret", "web/dist/assets/link"); err != nil {
		t.Fatal(err)
	}
	for target, want := range map[string]int{"/admin/": 200, "/admin/assets/app.js": 200, "/admin/assets/link": 404, "/admin/../secret": 404, "/web/secret": 404} {
		w := adminRequest(h, "GET", target, nil, "")
		if w.Code != want {
			t.Errorf("%s: %d, want %d", target, w.Code, want)
		}
	}
}

func TestAdminEmbeddedFS(t *testing.T) {
	st, packages := fixture(t, "[]", nil)
	h, err := New(st, packages)
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd)

	orig := AdminFS
	defer func() { AdminFS = orig }()

	AdminFS = fstest.MapFS{
		"index.html":       &fstest.MapFile{Data: []byte("embedded admin")},
		"assets/bundle.js": &fstest.MapFile{Data: []byte("console.log(1)")},
	}

	for target, want := range map[string]int{
		"/admin/":                  200,
		"/admin/index.html":        200,
		"/admin/assets/bundle.js":  200,
		"/admin/assets/missing.js": 404,
		"/admin/../secret":         404,
	} {
		w := adminRequest(h, "GET", target, nil, "")
		if w.Code != want {
			t.Errorf("%s: %d, want %d", target, w.Code, want)
		}
	}
	w := adminRequest(h, "GET", "/admin/", nil, "")
	if !strings.Contains(w.Body.String(), "embedded admin") {
		t.Fatalf("expected embedded admin body, got: %s", w.Body.String())
	}
}
