package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func zipFiles(t *testing.T, data []byte) map[string]string {
	t.Helper()
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	files := make(map[string]string)
	for _, f := range z.File {
		if _, exists := files[f.Name]; exists {
			t.Fatalf("duplicate archive member %q", f.Name)
		}
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(r)
		if closeErr := r.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			t.Fatal(err)
		}
		files[f.Name] = string(body)
	}
	return files
}

func TestE2E(t *testing.T) {
	dir := t.TempDir()
	server := filepath.Join(dir, "server")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if output, err := exec.CommandContext(ctx, "go", "build", "-o", server, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	library := filepath.Join(dir, "library")
	platformDir := filepath.Join(library, "Sony - PlayStation")
	if err := os.MkdirAll(platformDir, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(name, data string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(platformDir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cue := func(bin string) string { return "FILE \"" + bin + "\" BINARY\r\n  TRACK 01 MODE2/2352\r\n" }
	write("One (USA).cue", cue("One (USA).bin"))
	write("One (USA).bin", "one")
	write("Two (USA) (Disc 1).cue", cue("Two (USA) (Disc 1).bin"))
	write("Two (USA) (Disc 1).bin", "first disc")
	write("Two (USA) (Disc 2).cue", cue("Two (USA) (Disc 2).bin"))
	write("Two (USA) (Disc 2).bin", "second disc")
	write("readme.txt", "ignored")
	configPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(configPath, []byte("scan_interval = \"80ms\"\ncleanup_interval = \"80ms\"\ncache_max_age = \"24h\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(dir, "catalog.db")
	outputDir := filepath.Join(dir, "packages")
	run := func(args ...string) ([]byte, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, server, args...)
		cmd.Env = append(os.Environ(), "RETRO_CONFIG="+configPath)
		return cmd.Output()
	}
	t.Run("rejects missing database directory before serving", func(t *testing.T) {
		cmd := exec.Command(server, "serve", "-config", configPath, "-listen", "127.0.0.1:0", "-database", filepath.Join(dir, "missing-dir", "catalog.db"), "-packages", outputDir)
		output, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(output), "missing-dir") {
			t.Fatalf("missing directory: err=%v output=%s", err, output)
		}
	})
	type scanOutput struct {
		Added   []scanAdded   `json:"added"`
		Skipped []scanSkipped `json:"skipped"`
	}
	scan := func() scanOutput {
		t.Helper()
		output, err := run("scan", "-database", database, "-offline", library)
		if err != nil {
			t.Fatalf("scan: %v %s", err, output)
		}
		var result scanOutput
		if err := json.Unmarshal(output, &result); err != nil {
			t.Fatalf("scan output %s: %v", output, err)
		}
		return result
	}
	t.Run("scan adds games once", func(t *testing.T) {
		first := scan()
		if len(first.Added) != 2 || len(first.Skipped) != 0 {
			t.Fatalf("first scan: %+v", first)
		}
		for i, want := range []struct {
			id, name string
			files    int
		}{{"one-usa", "One (USA)", 2}, {"two-usa", "Two (USA)", 4}} {
			got := first.Added[i]
			if got.ID != want.id || got.CanonicalName != want.name || got.NameSource != "filename" || got.Platform != "Sony - PlayStation" || len(got.Files) != want.files {
				t.Fatalf("added %d: %+v", i, got)
			}
		}
		second := scan()
		if len(second.Added) != 0 || len(second.Skipped) != 2 || second.Skipped[0].Reason != "already in database" || second.Skipped[1].Reason != "already in database" {
			t.Fatalf("second scan: %+v", second)
		}
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(dir, "server.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd := exec.Command(server, "serve", "-config", configPath, "-listen", address, "-database", database, "-packages", outputDir)
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	base, _ := url.Parse("http://" + address + "/api/games")
	client := &http.Client{Timeout: time.Second}
	var resp *http.Response
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err = client.Get(base.String())
		if err == nil {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if err != nil {
		logData, _ := os.ReadFile(log.Name())
		t.Fatalf("server did not start: %v\n%s", err, logData)
	}
	resp.Body.Close()
	type card struct {
		ID          string   `json:"id"`
		Entrypoint  string   `json:"entrypoint"`
		Files       []string `json:"files"`
		DownloadURL string   `json:"download_url"`
	}
	getCards := func(query string) []card {
		t.Helper()
		u := *base
		u.RawQuery = query
		r, err := client.Get(u.String())
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		if r.StatusCode != http.StatusOK || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			t.Fatalf("catalog: status=%d type=%s", r.StatusCode, r.Header.Get("Content-Type"))
		}
		var cards []card
		if err := json.NewDecoder(r.Body).Decode(&cards); err != nil {
			t.Fatal(err)
		}
		if cards == nil {
			t.Fatal("expected JSON array, got null")
		}
		return cards
	}
	ids := func(query string) []string {
		t.Helper()
		result := []string{}
		for _, c := range getCards(query) {
			result = append(result, c.ID)
		}
		return result
	}
	waitIDs := func(query string, want []string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !reflect.DeepEqual(ids(query), want) {
			if time.Now().After(deadline) {
				t.Fatalf("catalog %q: %v, want %v", query, ids(query), want)
			}
			time.Sleep(80 * time.Millisecond)
		}
	}
	get := func(path string, status int) []byte {
		t.Helper()
		r, err := client.Get("http://" + address + path)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		data, err := io.ReadAll(r.Body)
		if err != nil || r.StatusCode != status {
			t.Fatalf("GET %s: status=%d err=%v %s", path, r.StatusCode, err, data)
		}
		return data
	}
	admin := func(method, path, etag string, body []byte) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest(method, "http://"+address+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://"+address)
		if etag != "" {
			req.Header.Set("If-Match", etag)
		}
		r, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		return r, data
	}
	cacheFiles := func() []string {
		t.Helper()
		files, err := filepath.Glob(filepath.Join(outputDir, ".cache", "*.zip"))
		if err != nil {
			t.Fatal(err)
		}
		return files
	}
	t.Run("catalog and search", func(t *testing.T) {
		if got := ids(""); !reflect.DeepEqual(got, []string{"one-usa", "two-usa"}) {
			t.Fatalf("catalog: %v", got)
		}
		for _, query := range []string{"q=TWO", "q=wo"} {
			if got := ids(query); !reflect.DeepEqual(got, []string{"two-usa"}) {
				t.Fatalf("search %s: %v", query, got)
			}
		}
		if got := ids("q=absent"); len(got) != 0 {
			t.Fatalf("unexpected matches: %v", got)
		}
	})
	if files := cacheFiles(); len(files) != 0 {
		t.Fatalf("catalog request created archives: %v", files)
	}
	wantTwo := map[string]string{
		"Two (USA).m3u":          "Two (USA) (Disc 1).cue\nTwo (USA) (Disc 2).cue\n",
		"Two (USA) (Disc 1).cue": cue("Two (USA) (Disc 1).bin"), "Two (USA) (Disc 1).bin": "first disc",
		"Two (USA) (Disc 2).cue": cue("Two (USA) (Disc 2).bin"), "Two (USA) (Disc 2).bin": "second disc",
	}
	t.Run("download generated multidisc package", func(t *testing.T) {
		cards := getCards("q=two")
		if len(cards) != 1 || cards[0].Entrypoint != "Two (USA).m3u" || cards[0].DownloadURL != "packages/two-usa.zip" || len(cards[0].Files) != len(wantTwo) {
			t.Fatalf("card: %+v", cards)
		}
		ref, _ := url.Parse(cards[0].DownloadURL)
		data := get(base.ResolveReference(ref).Path, http.StatusOK)
		cached := cacheFiles()
		if len(cached) != 1 {
			t.Fatalf("first download should cache one archive: %v", cached)
		}
		before, err := os.Stat(cached[0])
		if err != nil {
			t.Fatal(err)
		}
		if again := get(base.ResolveReference(ref).Path, http.StatusOK); !bytes.Equal(data, again) {
			t.Fatal("cached download differs")
		}
		after, err := os.Stat(cached[0])
		if err != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
			t.Fatalf("repeated download replaced cache: %v", err)
		}
		if files := zipFiles(t, data); !reflect.DeepEqual(files, wantTwo) {
			t.Fatalf("ZIP contents: %#v", files)
		}
		for _, f := range cards[0].Files {
			if _, ok := wantTwo[f]; !ok {
				t.Fatalf("unexpected card file %q", f)
			}
		}
	})
	t.Run("RetroArch content downloader", func(t *testing.T) {
		if dirs := string(get("/assets/cores/.index-dirs", http.StatusOK)); dirs != "Sony - PlayStation\n" {
			t.Fatalf("platform index: %q", dirs)
		}
		category := "/assets/cores/" + url.PathEscape("Sony - PlayStation")
		index := string(get(category+"/.index", http.StatusOK))
		if index != "One (USA).zip\nTwo (USA).zip\n" {
			t.Fatalf("content index: %q", index)
		}
		get(category+"/.index-dirs", http.StatusNotFound)
		files := zipFiles(t, get(category+"/"+url.PathEscape("Two (USA).zip"), http.StatusOK))
		want := map[string]string{}
		for name, data := range wantTwo {
			want["Two (USA)/"+name] = data
		}
		if !reflect.DeepEqual(files, want) {
			t.Fatalf("asset ZIP contents: %#v", files)
		}
		if files := cacheFiles(); len(files) != 1 {
			t.Fatalf("asset download did not reuse game cache: %v", files)
		}
	})
	t.Run("unknown route and methods", func(t *testing.T) {
		for _, tc := range []struct {
			method, path string
			status       int
		}{{"GET", "/api/packages/missing.zip", 404}, {"POST", "/api/games", 405}, {"POST", "/api/packages/two-usa.zip", 405}} {
			req, err := http.NewRequest(tc.method, "http://"+address+tc.path, nil)
			if err != nil {
				t.Fatal(err)
			}
			r, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			r.Body.Close()
			if r.StatusCode != tc.status || (tc.status == 405 && r.Header.Get("Allow") != "GET") {
				t.Fatalf("%s %s: status=%d Allow=%s", tc.method, tc.path, r.StatusCode, r.Header.Get("Allow"))
			}
		}
	})
	t.Run("admin edits a game", func(t *testing.T) {
		r, body := admin("GET", "/api/admin/games/two-usa", "", nil)
		revision := r.Header.Get("ETag")
		var current struct {
			Game map[string]any `json:"game"`
		}
		if r.StatusCode != http.StatusOK || revision == "" || json.Unmarshal(body, &current) != nil {
			t.Fatalf("read game: %d %q %s", r.StatusCode, revision, body)
		}
		current.Game["canonical_name"] = "Renamed Game"
		updated, _ := json.Marshal(current.Game)
		if r, body := admin("PUT", "/api/admin/games/two-usa", revision, updated); r.StatusCode != http.StatusNoContent {
			t.Fatalf("save: %d %s", r.StatusCode, body)
		}
		if got := ids("q=renamed"); !reflect.DeepEqual(got, []string{"two-usa"}) {
			t.Fatalf("updated catalog: %v", got)
		}
		if r, _ := admin("PUT", "/api/admin/games/two-usa", revision, updated); r.StatusCode != http.StatusPreconditionFailed {
			t.Fatalf("stale update: %d", r.StatusCode)
		}
	})
	t.Run("scheduled cache cleanup", func(t *testing.T) {
		get("/api/packages/one-usa.zip", http.StatusOK)
		files := cacheFiles()
		if len(files) != 2 {
			t.Fatalf("expected cached ZIPs: %v", files)
		}
		old := time.Now().Add(-25 * time.Hour)
		if err := os.Chtimes(files[0], old, old); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat(files[0]); os.IsNotExist(err) {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if _, err := os.Stat(files[0]); !os.IsNotExist(err) {
			t.Fatalf("old cached ZIP was not deleted: %v", err)
		}
		if _, err := os.Stat(files[1]); err != nil {
			t.Fatalf("recent cached ZIP was deleted: %v", err)
		}
	})
	t.Run("deleted source hides the game after a scan", func(t *testing.T) {
		if err := os.Remove(filepath.Join(platformDir, "One (USA).bin")); err != nil {
			t.Fatal(err)
		}
		time.Sleep(3 * 80 * time.Millisecond)
		waitIDs("", []string{"two-usa"})
	})
	t.Run("missing game becomes available", func(t *testing.T) {
		game := []byte(`{"id":"later","canonical_name":"Later Game","platform_id":"playstation","discs":[{"entrypoint":"game.txt","files":[{"source":"later/game.txt","path":"game.txt"}]}]}`)
		if r, body := admin("GET", "/api/admin/platforms", "", nil); r.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"id":"playstation"`)) {
			t.Fatalf("scan platform: %d %s", r.StatusCode, body)
		}
		r, body := admin("POST", "/api/admin/games", "", game)
		if r.StatusCode != http.StatusUnprocessableEntity || !bytes.Contains(body, []byte("later/game.txt")) {
			t.Fatalf("missing file warning: %d %s", r.StatusCode, body)
		}
		if r, _ := admin("GET", "/api/admin/games/later", "", nil); r.StatusCode != http.StatusNotFound {
			t.Fatalf("rejected game stored: %d", r.StatusCode)
		}
		if r, body := admin("POST", "/api/admin/games?allow_missing=1", "", game); r.StatusCode != http.StatusCreated {
			t.Fatalf("confirmed save: %d %s", r.StatusCode, body)
		}
		if got := ids("q=later"); len(got) != 0 {
			t.Fatalf("pending game exposed: %v", got)
		}
		if err := os.Mkdir(filepath.Join(dir, "later"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "later", "game.txt"), []byte("arrived"), 0600); err != nil {
			t.Fatal(err)
		}
		waitIDs("q=later", []string{"later"})
		if files := zipFiles(t, get("/api/packages/later.zip", http.StatusOK)); !reflect.DeepEqual(files, map[string]string{"game.txt": "arrived"}) {
			t.Fatalf("activated archive: %v", files)
		}
	})
}
