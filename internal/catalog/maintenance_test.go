package catalog

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"retroarch-marketplace/internal/content"
)

func TestMaintenanceCleanupSelection(t *testing.T) {
	manifest, packages := fixture(t, `[]`, nil)
	s, err := New(manifest, packages)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1700000000, 0)
	if err := s.cleanupCache(now, time.Hour); err != nil {
		t.Fatalf("missing cache: %v", err)
	}
	dir := filepath.Join(packages, ".cache")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	name := func(a, b string) string { return strings.Repeat(a, 64) + "-" + strings.Repeat(b, 64) + ".zip" }
	old, fresh, boundary := name("a", "1"), name("b", "2"), name("c", "3")
	link, directory := name("d", "4"), name("e", "5")
	unknown := []string{"user.zip", ".building-old", strings.ToUpper(name("f", "6")), old + ".tmp", strings.Repeat("g", 64) + "-" + strings.Repeat("1", 64) + ".zip"}
	for _, n := range append([]string{old, fresh, boundary}, unknown...) {
		path := filepath.Join(dir, n)
		if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
		stamp := now.Add(-2 * time.Hour)
		if n == fresh {
			stamp = now.Add(-time.Minute)
		} else if n == boundary {
			stamp = now.Add(-time.Hour)
		}
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.zip")
	if err := os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, link)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, directory), 0755); err != nil {
		t.Fatal(err)
	}
	userZip := filepath.Join(packages, "user.zip")
	if err := os.WriteFile(userZip, []byte("user"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.cleanupCache(now, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dir, old)); !os.IsNotExist(err) {
		t.Fatalf("expired entry remains: %v", err)
	}
	for _, n := range append([]string{fresh, boundary, link, directory}, unknown...) {
		if _, err := os.Lstat(filepath.Join(dir, n)); err != nil {
			t.Errorf("retained entry %s: %v", n, err)
		}
	}
	for _, path := range []string{outside, userZip} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("external file removed: %v", err)
		}
	}
}

func TestMaintenanceRejectsSymlinkDirectories(t *testing.T) {
	for _, rootLink := range []bool{false, true} {
		t.Run(map[bool]string{false: "cache", true: "root"}[rootLink], func(t *testing.T) {
			base := t.TempDir()
			outside := t.TempDir()
			packages := filepath.Join(base, "packages")
			var link string
			if rootLink {
				link = packages
			} else {
				if err := os.Mkdir(packages, 0755); err != nil {
					t.Fatal(err)
				}
				link = filepath.Join(packages, ".cache")
			}
			if err := os.Symlink(outside, link); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(outside, strings.Repeat("a", 64)+"-"+strings.Repeat("b", 64)+".zip")
			if rootLink {
				if err := os.Mkdir(filepath.Join(outside, ".cache"), 0755); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(outside, ".cache", filepath.Base(path))
			}
			if err := os.WriteFile(path, []byte("outside"), 0600); err != nil {
				t.Fatal(err)
			}
			s := &Server{packagesDir: packages}
			if err := s.cleanupCache(time.Now().Add(48*time.Hour), time.Hour); err == nil {
				t.Fatal("accepted symlink directory")
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("outside file changed: %v", err)
			}
		})
	}
}

func TestMaintenanceChecksSources(t *testing.T) {
	manifest, packages := fixture(t, "["+card+"]", map[string][]zipEntry{"example": {{name: "game.txt", data: "first"}}})
	s, err := New(manifest, packages)
	if err != nil {
		t.Fatal(err)
	}
	original := s.snapshot.fingerprint
	source := filepath.Join(filepath.Dir(manifest), "source-0-0")
	if err := os.WriteFile(source, []byte("updated source"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.checkSources(); err != nil {
		t.Fatal(err)
	}
	if s.snapshot.fingerprint == original {
		t.Fatal("source change not detected")
	}
	if entries := cacheEntries(t, packages); len(entries) != 0 {
		t.Fatalf("scan created cache entries: %v", entries)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err := s.checkSources(); err == nil || s.scanErr == nil {
		t.Fatal("missing source not recorded")
	}
	if w := request(t, s, http.MethodGet, "/api/games"); w.Code != http.StatusInternalServerError {
		t.Fatalf("HTTP must still report a missing source: %d", w.Code)
	}
	if err := os.WriteFile(source, []byte("restored"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.checkSources(); err != nil || s.scanErr != nil {
		t.Fatalf("recovery failed: %v", err)
	}
}

func TestMaintenancePeriodicAndCancellation(t *testing.T) {
	manifest, packages := fixture(t, "["+card+"]", map[string][]zipEntry{"example": {{name: "game.txt", data: "first"}}})
	s, err := New(manifest, packages)
	if err != nil {
		t.Fatal(err)
	}
	original := s.snapshot.fingerprint
	if err := os.WriteFile(filepath.Join(filepath.Dir(manifest), "source-0-0"), []byte("changed source"), 0600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(packages, ".cache")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, strings.Repeat("a", 64)+"-"+strings.Repeat("b", 64)+".zip")
	if err := os.WriteFile(old, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(old, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.RunMaintenance(ctx, 5*time.Millisecond, 7*time.Millisecond, time.Hour)
	}()
	defer cancel()
	deadline := time.After(3 * time.Second)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		s.mu.Lock()
		changed := s.snapshot.fingerprint != original
		s.mu.Unlock()
		_, statErr := os.Stat(old)
		if changed && os.IsNotExist(statErr) {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("periodic work not observed: changed=%v stat=%v", changed, statErr)
		case <-tick.C:
		}
	}
	if entries := cacheEntries(t, packages); len(entries) != 0 {
		t.Fatalf("scan created archives: %v", entries)
	}
	source := filepath.Join(filepath.Dir(manifest), "source-0-0")
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	waitScanError := func(wantError bool) {
		t.Helper()
		timeout := time.After(3 * time.Second)
		for {
			s.mu.Lock()
			hasError := s.scanErr != nil
			s.mu.Unlock()
			if hasError == wantError {
				return
			}
			select {
			case <-timeout:
				t.Fatalf("periodic scan error state did not become %v", wantError)
			case <-tick.C:
			}
		}
	}
	waitScanError(true)
	if err := os.WriteFile(source, []byte("restored source"), 0600); err != nil {
		t.Fatal(err)
	}
	waitScanError(false)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("maintenance did not stop")
	}
}

type pausedWriter struct {
	*httptest.ResponseRecorder
	started chan struct{}
	resume  chan struct{}
	once    sync.Once
}

func (w *pausedWriter) Write(data []byte) (int, error) {
	w.once.Do(func() { close(w.started); <-w.resume })
	return w.ResponseRecorder.Write(data)
}

func TestMaintenancePreservesActiveDownloads(t *testing.T) {
	for _, asset := range []bool{false, true} {
		t.Run(map[bool]string{false: "package", true: "asset"}[asset], func(t *testing.T) {
			second := strings.Replace(card, `"example"`, `"second"`, 1)
			manifest, packages := fixture(t, "["+card+","+second+"]", map[string][]zipEntry{
				"example": {{name: "game.txt", data: "first source"}}, "second": {{name: "game.txt", data: "second source"}},
			})
			s, err := New(manifest, packages)
			if err != nil {
				t.Fatal(err)
			}
			target := "/api/packages/example.zip"
			if asset {
				target = "/assets/cores/" + url.PathEscape("Sony - PlayStation") + "/example.zip"
			}
			w := &pausedWriter{ResponseRecorder: httptest.NewRecorder(), started: make(chan struct{}), resume: make(chan struct{})}
			done := make(chan struct{})
			go func() { defer close(done); s.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil)) }()
			resumed := false
			defer func() {
				if !resumed {
					close(w.resume)
					<-done
				}
			}()
			select {
			case <-w.started:
			case <-time.After(3 * time.Second):
				t.Fatal("download did not start")
			}
			entries := cacheEntries(t, packages)
			if len(entries) != 1 {
				t.Fatalf("expected active archive, got %v", entries)
			}
			path := filepath.Join(packages, ".cache", entries[0].Name())
			old := time.Now().Add(-2 * time.Hour)
			if err := os.Chtimes(path, old, old); err != nil {
				t.Fatal(err)
			}
			if err := s.cleanupCache(time.Now(), time.Hour); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("active archive removed: %v", err)
			}
			other := make(chan int, 1)
			go func() {
				r := httptest.NewRecorder()
				s.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/api/packages/second.zip", nil))
				other <- r.Code
			}()
			select {
			case status := <-other:
				if status != http.StatusOK {
					t.Fatalf("other download: %d", status)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("active download blocked an unrelated download")
			}
			close(w.resume)
			resumed = true
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("download did not finish")
			}
			if w.Code != http.StatusOK {
				t.Fatalf("download status: %d", w.Code)
			}
			if _, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len())); err != nil {
				t.Fatalf("downloaded archive is invalid: %v", err)
			}
			if err := s.cleanupCache(time.Now(), time.Hour); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("archive was not removed after download: %v", err)
			}
		})
	}
}

func waitArchiveLeases(t *testing.T, s *Server, path string, count int) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		s.cacheMu.Lock()
		n := s.active[path]
		s.cacheMu.Unlock()
		if n == count {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("leases for %s: got %d, want %d", path, n, count)
		default:
			runtime.Gosched()
		}
	}
}

func TestConcurrentArchiveHTTP(t *testing.T) {
	second := strings.Replace(card, `"example"`, `"second"`, 1)
	third := strings.Replace(card, `"example"`, `"third"`, 1)
	manifest, packages := fixture(t, "["+card+","+second+","+third+"]", map[string][]zipEntry{
		"example": {{name: "game.txt", data: "first"}}, "second": {{name: "game.txt", data: "second"}}, "third": {{name: "game.txt", data: "third"}},
	})
	s, err := New(manifest, packages)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.load()
	if err != nil {
		t.Fatal(err)
	}
	ready, err := s.archive(snapshot.fingerprint, snapshot.packages["/api/packages/third.zip"])
	if err != nil {
		t.Fatal(err)
	}
	s.releaseArchive(ready)
	started := make(chan string, 4)
	firstGate, secondGate := make(chan struct{}), make(chan struct{})
	s.writeZIP = func(item content.Item, path string) error {
		started <- item.ID
		switch item.ID {
		case "example":
			<-firstGate
		case "second":
			<-secondGate
		}
		return item.WriteZIP(path)
	}
	server := httptest.NewServer(s)
	defer server.Close()
	var firstOnce, secondOnce sync.Once
	releaseFirst := func() { firstOnce.Do(func() { close(firstGate) }) }
	releaseSecond := func() { secondOnce.Do(func() { close(secondGate) }) }
	defer releaseFirst()
	defer releaseSecond()
	waitStarted := func() string {
		t.Helper()
		select {
		case id := <-started:
			return id
		case <-time.After(5 * time.Second):
			t.Fatal("archive build did not start")
			return ""
		}
	}
	type result struct {
		body   []byte
		err    error
		status int
	}
	get := func(target string) <-chan result {
		done := make(chan result, 1)
		go func() {
			resp, err := http.Get(server.URL + target)
			if err != nil {
				done <- result{err: err}
				return
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			done <- result{body, err, resp.StatusCode}
		}()
		return done
	}
	await := func(ch <-chan result) result {
		t.Helper()
		select {
		case result := <-ch:
			if result.err != nil || result.status != http.StatusOK {
				t.Fatalf("HTTP: %d %v", result.status, result.err)
			}
			if _, err := zip.NewReader(bytes.NewReader(result.body), int64(len(result.body))); err != nil {
				t.Fatal(err)
			}
			return result
		case <-time.After(5 * time.Second):
			t.Fatal("HTTP request blocked")
			return result{}
		}
	}
	first := get("/api/packages/example.zip")
	if id := waitStarted(); id != "example" {
		t.Fatal(id)
	}
	s.cacheMu.Lock()
	var firstPath string
	for p := range s.builds {
		firstPath = p
	}
	s.cacheMu.Unlock()
	asset := get("/assets/cores/" + url.PathEscape("Sony - PlayStation") + "/example.zip")
	waitArchiveLeases(t, s, firstPath, 2)
	await(get("/api/packages/third.zip"))
	independent := get("/api/packages/second.zip")
	if id := waitStarted(); id != "second" {
		t.Fatal(id)
	}
	releaseSecond()
	await(independent)
	if err := os.WriteFile(firstPath, []byte("temporary test entry"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(firstPath, old, old); err != nil {
		t.Fatal(err)
	}
	if err := s.cleanupCache(time.Now(), time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(firstPath); err != nil {
		t.Fatalf("building archive removed: %v", err)
	}
	releaseFirst()
	a, b := await(first), await(asset)
	if bytes.Equal(a.body, b.body) {
		t.Fatal("asset ZIP should contain a prefixed path")
	}
	for _, check := range []struct {
		body []byte
		name string
	}{{a.body, "game.txt"}, {b.body, assetLabel("Étoile Game") + "-6578616d706c65/game.txt"}} {
		z, err := zip.NewReader(bytes.NewReader(check.body), int64(len(check.body)))
		if err != nil || len(z.File) != 1 {
			t.Fatalf("unexpected ZIP: %v, %v", z, err)
		}
		if z.File[0].Name != check.name {
			t.Fatalf("member %q, want %q", z.File[0].Name, check.name)
		}
		r, err := z.File[0].Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		r.Close()
		if err != nil || string(data) != "first" {
			t.Fatalf("ZIP data %q: %v", data, err)
		}
	}
	select {
	case id := <-started:
		t.Fatalf("duplicate build: %s", id)
	default:
	}
	if err := s.cleanupCache(time.Now(), time.Hour); err != nil {
		t.Fatal(err)
	}
}

func TestArchiveFailureWakesWaitersAndRetries(t *testing.T) {
	for _, change := range []bool{false, true} {
		name := "write error"
		if change {
			name = "fingerprint change"
		}
		t.Run(name, func(t *testing.T) {
			manifest, packages := fixture(t, "["+card+"]", map[string][]zipEntry{"example": {{name: "game.txt", data: "first"}}})
			s, err := New(manifest, packages)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := s.load()
			if err != nil {
				t.Fatal(err)
			}
			item := snapshot.packages["/api/packages/example.zip"]
			started, gate := make(chan struct{}), make(chan struct{})
			s.writeZIP = func(item content.Item, path string) error {
				close(started)
				<-gate
				if !change {
					return errors.New("write failed")
				}
				return item.WriteZIP(path)
			}
			type result struct {
				path string
				err  error
			}
			call := func() <-chan result {
				ch := make(chan result, 1)
				go func() { p, e := s.archive(snapshot.fingerprint, item); ch <- result{p, e} }()
				return ch
			}
			first := call()
			<-started
			s.cacheMu.Lock()
			var path string
			for p := range s.builds {
				path = p
			}
			s.cacheMu.Unlock()
			second := call()
			waitArchiveLeases(t, s, path, 2)
			if change {
				data, err := os.ReadFile(manifest)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(manifest, append(data, ' '), 0600); err != nil {
					t.Fatal(err)
				}
			}
			close(gate)
			for _, ch := range []<-chan result{first, second} {
				select {
				case r := <-ch:
					if r.err == nil {
						t.Fatal("expected build error")
					}
				case <-time.After(5 * time.Second):
					t.Fatal("waiter blocked")
				}
			}
			s.cacheMu.Lock()
			if len(s.active) != 0 || len(s.builds) != 0 {
				t.Fatal("leases or build leaked")
			}
			s.cacheMu.Unlock()
			s.writeZIP = nil
			current, err := s.load()
			if err != nil {
				t.Fatal(err)
			}
			p, err := s.archive(current.fingerprint, current.packages["/api/packages/example.zip"])
			if err != nil {
				t.Fatalf("retry: %v", err)
			}
			s.releaseArchive(p)
			z, err := zip.OpenReader(p)
			if err != nil {
				t.Fatal(err)
			}
			z.Close()
		})
	}
}
