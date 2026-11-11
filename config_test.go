package main

import (
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func configEnv(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) { value, ok := values[key]; return value, ok }
}

func TestConfigPrecedence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom.toml")
	data := `listen = "file:1"
database = "file.db"
packages = "file-packages"
scan_interval = "11m"
cleanup_interval = "2h"
cache_max_age = "25h"
`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	env := configEnv(map[string]string{
		"RETRO_CONFIG": path, "RETRO_LISTEN": "env:2", "RETRO_DATABASE": "env.db",
		"RETRO_PACKAGES": "env-packages", "RETRO_SCAN_INTERVAL": "12m",
		"RETRO_CLEANUP_INTERVAL": "3h", "RETRO_CACHE_MAX_AGE": "26h",
	})
	got, _, err := loadConfig("serve", []string{"-listen=cli:3", "-database=cli.db", "-packages=cli-packages", "-scan-interval=13m", "-cleanup-interval=4h", "-cache-max-age=27h"}, env, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := config{"cli:3", "cli.db", "cli-packages", 13 * time.Minute, 4 * time.Hour, 27 * time.Hour, nil}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	got, _, err = loadConfig("serve", nil, env, nil)
	if err != nil {
		t.Fatal(err)
	}
	want = config{"env:2", "env.db", "env-packages", 12 * time.Minute, 3 * time.Hour, 26 * time.Hour, nil}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	got, _, err = loadConfig("serve", []string{"-config", path}, configEnv(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	want = config{"file:1", "file.db", "file-packages", 11 * time.Minute, 2 * time.Hour, 25 * time.Hour, nil}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	got, _, err = loadConfig("serve", []string{"-config", path}, configEnv(map[string]string{"RETRO_CONFIG": filepath.Join(t.TempDir(), "missing")}), nil)
	if err != nil || got != want {
		t.Fatalf("config flag did not override environment: %+v, %v", got, err)
	}
}

func TestConfigErrors(t *testing.T) {
	dir := t.TempDir()
	for _, test := range []struct{ name, data string }{
		{"malformed", "listen = ["}, {"unknown", "unknown = 1"}, {"removed catalog key", `catalog = "catalog-source.json"`},
		{"wrong type", "scan_interval = 10"}, {"invalid duration", "scan_interval = \"bad\""},
		{"zero duration", "cleanup_interval = \"0s\""}, {"negative duration", "cache_max_age = \"-1h\""},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(test.name, " ", "-"))
			if err := os.WriteFile(path, []byte(test.data), 0600); err != nil {
				t.Fatal(err)
			}
			if _, _, err := loadConfig("serve", []string{"-config", path}, configEnv(nil), nil); err == nil {
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
		if _, _, err := loadConfig("serve", test.args, configEnv(test.env), nil); err == nil {
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
	got, _, err := loadConfig("serve", nil, configEnv(nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	want := config{"127.0.0.1:5050", "catalog.db", "packages", 10 * time.Minute, time.Hour, 24 * time.Hour, nil}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
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

func TestBoxArtDistanceConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "boxart.toml")
	if err := os.WriteFile(path, []byte("boxart_max_distance = 2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		override string
		args     []string
		want     int
	}{
		{"", nil, 2},
		{"1", nil, 1},
		{"1", []string{"-boxart-max-distance=0"}, 0},
	} {
		env := map[string]string{"RETRO_CONFIG": path}
		if tc.override != "" {
			env["RETRO_BOXART_MAX_DISTANCE"] = tc.override
		}
		cfg, _, err := loadConfig("serve", tc.args, configEnv(env), nil)
		if err != nil || cfg.BoxArtMaxDistance == nil || *cfg.BoxArtMaxDistance != tc.want {
			t.Fatalf("args %v, env %q: got %v, want %d", tc.args, tc.override, err, tc.want)
		}
	}
	for _, invalid := range []string{"-1", "1.5", "abc"} {
		if _, _, err := loadConfig("serve", []string{"-config", path, "-boxart-max-distance=" + invalid}, configEnv(nil), nil); err == nil {
			t.Fatalf("accepted invalid BoxArt distance %q", invalid)
		}
	}
}
