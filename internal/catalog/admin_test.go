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

func TestAdminManifestUpdatesAndRepair(t *testing.T) {
	manifest, packages := fixture(t, "["+card+"]", map[string][]zipEntry{"example": {{name: "game.txt", data: "source bytes"}}})
	h, err := New(manifest, packages)
	if err != nil {
		t.Fatal(err)
	}
	get := adminRequest(h, "GET", "/api/admin/manifest", nil, "")
	if get.Code != 200 || get.Header().Get("ETag") != manifestETag(get.Body.Bytes()) {
		t.Fatalf("GET: %d %s", get.Code, get.Body.String())
	}
	if get.Header().Get("X-Catalog-Version") != Version {
		t.Fatalf("version header: got %q, want %q", get.Header().Get("X-Catalog-Version"), Version)
	}
	original := append([]byte(nil), get.Body.Bytes()...)
	etag := get.Header().Get("ETag")
	candidate := bytes.Replace(original, []byte("Étoile Game"), []byte("Renamed Game"), 1)
	for _, tc := range []struct {
		body   []byte
		tag    string
		status int
	}{
		{candidate, "", 412}, {candidate, `"stale"`, 412}, {[]byte(`{"platforms":[]}`), etag, 400},
	} {
		w := adminRequest(h, "PUT", "/api/admin/manifest", tc.body, tc.tag)
		if w.Code != tc.status {
			t.Errorf("PUT: %d %s", w.Code, w.Body.String())
		}
		got, err := os.ReadFile(manifest)
		if err != nil || !bytes.Equal(got, original) {
			t.Fatalf("original changed: %v", err)
		}
	}
	w := adminRequest(h, "PUT", "/api/admin/manifest", candidate, etag)
	if w.Code != 204 || w.Header().Get("ETag") != manifestETag(candidate) {
		t.Fatalf("PUT: %d %s", w.Code, w.Body.String())
	}
	info, err := os.Stat(manifest)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("permissions: %v %v", info, err)
	}
	source, err := os.ReadFile(filepath.Join(filepath.Dir(manifest), "source-0-0"))
	if err != nil || string(source) != "source bytes" {
		t.Fatalf("source changed: %q %v", source, err)
	}
	games := request(t, h, "GET", "/api/games?q=Renamed")
	if games.Code != 200 || !strings.Contains(games.Body.String(), "Renamed Game") {
		t.Fatalf("catalog: %d %s", games.Code, games.Body.String())
	}
	if w = adminRequest(h, "PUT", "/api/admin/manifest", original, etag); w.Code != 412 {
		t.Fatalf("stale: %d", w.Code)
	}
	if err := os.WriteFile(manifest, []byte("not json"), 0600); err != nil {
		t.Fatal(err)
	}
	get = adminRequest(h, "GET", "/api/admin/manifest", nil, "")
	if get.Code != 200 || get.Body.String() != "not json" {
		t.Fatalf("invalid GET: %d %s", get.Code, get.Body.String())
	}
	w = adminRequest(h, "PUT", "/api/admin/manifest", candidate, get.Header().Get("ETag"))
	if w.Code != 204 {
		t.Fatalf("repair: %d %s", w.Code, w.Body.String())
	}
}

func TestAdminSecurity(t *testing.T) {
	manifest, packages := fixture(t, "[]", nil)
	h, err := New(manifest, packages)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, remote, host, origin, contentType, method, target string
		status                                                  int
	}{
		{"remote", "192.0.2.1:42", "localhost:5050", "http://localhost:5050", "application/json", "PUT", "/api/admin/manifest", 403},
		{"host", "127.0.0.1:42", "evil.test:5050", "http://evil.test:5050", "application/json", "PUT", "/api/admin/manifest", 403},
		{"origin missing", "127.0.0.1:42", "localhost:5050", "", "application/json", "PUT", "/api/admin/manifest", 403},
		{"origin cross", "127.0.0.1:42", "localhost:5050", "http://evil.test", "application/json", "PUT", "/api/admin/manifest", 403},
		{"forced origin cross", "127.0.0.1:42", "localhost:5050", "http://evil.test", "application/json", "PUT", "/api/admin/manifest?allow_missing=1", 403},
		{"origin scheme", "127.0.0.1:42", "localhost:5050", "https://localhost:5050", "application/json", "PUT", "/api/admin/manifest", 403},
		{"content type", "127.0.0.1:42", "localhost:5050", "http://localhost:5050", "text/plain", "PUT", "/api/admin/manifest", 400},
		{"method", "127.0.0.1:42", "localhost:5050", "", "", "POST", "/api/admin/manifest", 405},
		{"static remote", "192.0.2.1:42", "localhost:5050", "", "", "GET", "/admin/", 403},
		{"static host", "127.0.0.1:42", "evil.test", "", "", "GET", "/admin/", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "http://localhost:5050"+tc.target, bytes.NewReader(original))
			r.RemoteAddr, r.Host = tc.remote, tc.host
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Content-Type", tc.contentType)
			r.Header.Set("If-Match", manifestETag(original))
			r.Header.Set("X-Forwarded-For", "127.0.0.1")
			r.Header.Set("X-Forwarded-Host", "localhost:5050")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
		})
	}

	w := adminRequest(h, "PUT", "/api/admin/manifest", bytes.Repeat([]byte("x"), maxManifestSize+1), manifestETag(original))
	if w.Code != 400 {
		t.Fatalf("oversized: %d", w.Code)
	}
	got, err := os.ReadFile(manifest)
	if err != nil || !bytes.Equal(got, original) {
		t.Fatalf("manifest changed: %v", err)
	}
}
func TestAdminConcurrentUpdates(t *testing.T) {
	manifest, packages := fixture(t, "[]", nil)
	h, err := New(manifest, packages)
	if err != nil {
		t.Fatal(err)
	}
	get := adminRequest(h, "GET", "/api/admin/manifest", nil, "")
	results := make(chan int, 2)
	for _, candidate := range [][]byte{[]byte(`{"platforms":[],"contents":[]} `), []byte(`{"platforms":[],"contents":[]}`)} {
		go func(body []byte) {
			results <- adminRequest(h, "PUT", "/api/admin/manifest", body, get.Header().Get("ETag")).Code
		}(candidate)
	}
	first, second := <-results, <-results
	if !((first == 204 && second == 412) || (first == 412 && second == 204)) {
		t.Fatalf("concurrent statuses: %d, %d", first, second)
	}
}

func TestAdminSymlinkManifest(t *testing.T) {
	manifest, packages := fixture(t, "[]", nil)
	link := filepath.Join(filepath.Dir(manifest), "link.json")
	if err := os.Symlink(manifest, link); err != nil {
		t.Fatal(err)
	}
	h, err := New(link, packages)
	if err != nil {
		t.Fatal(err)
	}
	get := adminRequest(h, "GET", "/api/admin/manifest", nil, "")
	w := adminRequest(h, "PUT", "/api/admin/manifest", get.Body.Bytes(), get.Header().Get("ETag"))
	if w.Code != 403 {
		t.Fatalf("symlink PUT: %d %s", w.Code, w.Body.String())
	}
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink replaced: %v", err)
	}
}

func TestAdminAllowsSymlinkedSources(t *testing.T) {
	manifest, packages := fixture(t, "["+card+"]", map[string][]zipEntry{"example": {{name: "game.txt", data: "source bytes"}}})
	source := filepath.Join(filepath.Dir(manifest), "source-0-0")
	if err := os.Rename(source, source+"-real"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source+"-real", source); err != nil {
		t.Fatal(err)
	}
	h, err := New(manifest, packages)
	if err != nil {
		t.Fatal(err)
	}
	get := adminRequest(h, "GET", "/api/admin/manifest", nil, "")
	bad := bytes.Replace(get.Body.Bytes(), []byte("source-0-0"), []byte("missing-source"), 1)
	w := adminRequest(h, "PUT", "/api/admin/manifest", bad, get.Header().Get("ETag"))
	if w.Code != 422 || !strings.Contains(w.Body.String(), "missing-source") {
		t.Fatalf("missing source: %d %s", w.Code, w.Body.String())
	}
	updated := bytes.Replace(get.Body.Bytes(), []byte("Étoile Game"), []byte("Symlinked"), 1)
	w = adminRequest(h, "PUT", "/api/admin/manifest", updated, get.Header().Get("ETag"))
	if w.Code != 204 {
		t.Fatalf("symlink source: %d %s", w.Code, w.Body.String())
	}
}

func TestAdminStatic(t *testing.T) {
	manifest, packages := fixture(t, "[]", nil)
	h, err := New(manifest, packages)
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
	manifest, packages := fixture(t, "[]", nil)
	h, err := New(manifest, packages)
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
