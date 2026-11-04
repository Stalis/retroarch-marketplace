package catalog

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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
