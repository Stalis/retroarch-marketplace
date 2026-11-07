package main

import (
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"retroarch-marketplace/internal/catalog"
)

func configEnv(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) { value, ok := values[key]; return value, ok }
}

func TestConfigPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom.toml")
	data := `listen = "file:1"
catalog = "file.json"
packages = "file-packages"
scan_interval = "11m"
cleanup_interval = "2h"
cache_max_age = "25h"
`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	env := configEnv(map[string]string{
		"RETRO_CONFIG": path, "RETRO_LISTEN": "env:2", "RETRO_CATALOG": "env.json",
		"RETRO_PACKAGES": "env-packages", "RETRO_SCAN_INTERVAL": "12m",
		"RETRO_CLEANUP_INTERVAL": "3h", "RETRO_CACHE_MAX_AGE": "26h",
	})
	got, err := loadConfig([]string{"-listen=cli:3", "-catalog=cli.json", "-packages=cli-packages", "-scan-interval=13m", "-cleanup-interval=4h", "-cache-max-age=27h"}, env)
	if err != nil {
		t.Fatal(err)
	}
	want := config{"cli:3", "cli.json", "cli-packages", 13 * time.Minute, 4 * time.Hour, 27 * time.Hour, false}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	got, err = loadConfig(nil, env)
	if err != nil {
		t.Fatal(err)
	}
	want = config{"env:2", "env.json", "env-packages", 12 * time.Minute, 3 * time.Hour, 26 * time.Hour, false}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	got, err = loadConfig([]string{"-config", path}, configEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	want = config{"file:1", "file.json", "file-packages", 11 * time.Minute, 2 * time.Hour, 25 * time.Hour, false}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	got, err = loadConfig([]string{"-config", path}, configEnv(map[string]string{"RETRO_CONFIG": filepath.Join(t.TempDir(), "missing")}))
	if err != nil || got != want {
		t.Fatalf("config flag did not override environment: %+v, %v", got, err)
	}
}

func TestConfigErrors(t *testing.T) {
	dir := t.TempDir()
	for _, test := range []struct{ name, data string }{
		{"malformed", "listen = ["}, {"unknown", "unknown = 1"},
		{"wrong type", "scan_interval = 10"}, {"invalid duration", "scan_interval = \"bad\""},
		{"zero duration", "cleanup_interval = \"0s\""}, {"negative duration", "cache_max_age = \"-1h\""},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(test.name, " ", "-"))
			if err := os.WriteFile(path, []byte(test.data), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadConfig([]string{"-config", path}, configEnv(nil)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	missing := filepath.Join(dir, "missing")
	for _, test := range []struct {
		args []string
		env  map[string]string
	}{
		{[]string{"-config", missing}, nil}, {nil, map[string]string{"RETRO_CONFIG": missing}},
		{[]string{"-config", ""}, nil}, {nil, map[string]string{"RETRO_CONFIG": ""}},
		{[]string{"-scan-interval=0s"}, nil}, {nil, map[string]string{"RETRO_CACHE_MAX_AGE": "invalid"}},
		{[]string{"-bad-flag"}, nil},
	} {
		if _, err := loadConfig(test.args, configEnv(test.env)); err == nil {
			t.Fatalf("expected error for args %v env %v", test.args, test.env)
		}
	}
}

func TestConfigDefaultsAndOptionalFile(t *testing.T) {
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Error(err)
		}
	})
	got, err := loadConfig(nil, configEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	want := config{"127.0.0.1:5050", "catalog-source.json", "packages", 10 * time.Minute, time.Hour, 24 * time.Hour, true}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestConfigVersion(t *testing.T) {
	_, err := loadConfig([]string{"-version"}, configEnv(nil))
	if err != errVersion {
		t.Fatalf("expected errVersion, got: %v", err)
	}
}

func TestDefaultCatalogSelection(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(file, []byte("catalog = \"catalog-source.json\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		args []string
		env  map[string]string
	}{
		{"file", []string{"-config", file}, nil},
		{"environment", nil, map[string]string{"RETRO_CATALOG": "catalog-source.json"}},
		{"flag", []string{"-catalog", "catalog-source.json"}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := loadConfig(tc.args, configEnv(tc.env))
			if err != nil || cfg.initDefaultCatalog {
				t.Fatalf("explicit catalog: %+v, %v", cfg, err)
			}
		})
	}
	missing := filepath.Join(dir, "missing.json")
	cfg, err := loadConfig([]string{"-catalog", missing}, configEnv(nil))
	if err != nil || cfg.initDefaultCatalog {
		t.Fatalf("explicit missing catalog: %+v, %v", cfg, err)
	}
	if _, err := catalog.New(cfg.Catalog, filepath.Join(dir, "packages")); err == nil {
		t.Fatal("explicit missing catalog accepted")
	}
	if _, err := os.Lstat(missing); !os.IsNotExist(err) {
		t.Fatalf("explicit missing catalog was created: %v", err)
	}
}

func TestInitDefaultCatalog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog-source.json")
	const empty = `{"platforms":[],"contents":[]}`
	if err := initDefaultCatalog(path); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != empty {
		t.Fatalf("initial catalog = %q, %v", data, err)
	}
	if _, err := catalog.New(path, filepath.Join(dir, "packages")); err != nil {
		t.Fatal(err)
	}
	for _, existing := range []string{empty, "invalid"} {
		if err := os.WriteFile(path, []byte(existing), 0600); err != nil {
			t.Fatal(err)
		}
		if err := initDefaultCatalog(path); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil || string(data) != existing {
			t.Fatalf("existing catalog changed: %q, %v", data, err)
		}
		if existing == "invalid" {
			if _, err := catalog.New(path, filepath.Join(dir, "packages")); err == nil {
				t.Fatal("invalid catalog accepted")
			}
		}
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(filepath.Join(dir, "missing"), link); err != nil {
		t.Fatal(err)
	}
	if err := initDefaultCatalog(link); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("symlink changed: %v, %v", info, err)
	}
	if err := initDefaultCatalog(filepath.Join(dir, "missing", "catalog.json")); err == nil {
		t.Fatal("missing parent accepted")
	}
}

func TestInitDefaultCatalogConcurrent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog-source.json")
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := initDefaultCatalog(path); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if _, err := catalog.New(path, filepath.Join(dir, "packages")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary files remain: %v, %v", entries, err)
	}
}

func TestInitDefaultCatalogUnwritable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write to a read-only directory")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
	if err := initDefaultCatalog(filepath.Join(dir, "catalog-source.json")); err == nil {
		t.Fatal("unwritable directory accepted")
	}
}

func TestHTTPHeaderTimeoutAndSlowBody(t *testing.T) {
	const timeout = 100 * time.Millisecond
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{
		ReadHeaderTimeout: timeout,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/slow" {
				_, _ = io.WriteString(w, "first")
				w.(http.Flusher).Flush()
				time.Sleep(3 * timeout)
				_, _ = io.WriteString(w, "second")
				return
			}
			_, _ = io.WriteString(w, "ok")
		}),
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	defer func() { server.Close(); <-done }()
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: localhost\r\n"); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(conn)
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("incomplete headers were not timed out")
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 0 && !strings.Contains(string(data), "408") {
		t.Fatalf("unexpected response %q", data)
	}
	for _, tc := range []struct{ path, want string }{{"/", "ok"}, {"/slow", "firstsecond"}} {
		resp, err := http.Get("http://" + listener.Addr().String() + tc.path)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK || string(body) != tc.want {
			t.Fatalf("%s: status %d body %q: %v", tc.path, resp.StatusCode, body, err)
		}
	}
}
