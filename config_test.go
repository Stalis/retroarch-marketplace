package main

import (
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
	want := config{"cli:3", "cli.json", "cli-packages", 13 * time.Minute, 4 * time.Hour, 27 * time.Hour}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	got, err = loadConfig(nil, env)
	if err != nil {
		t.Fatal(err)
	}
	want = config{"env:2", "env.json", "env-packages", 12 * time.Minute, 3 * time.Hour, 26 * time.Hour}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	got, err = loadConfig([]string{"-config", path}, configEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	want = config{"file:1", "file.json", "file-packages", 11 * time.Minute, 2 * time.Hour, 25 * time.Hour}
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
	want := config{"127.0.0.1:5050", "catalog-source.json", "packages", 10 * time.Minute, time.Hour, 24 * time.Hour}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
