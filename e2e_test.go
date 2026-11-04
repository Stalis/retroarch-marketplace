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

func TestE2E(t *testing.T) {
	dir := t.TempDir()
	build := func(name, pkg string) string {
		t.Helper()
		binary := filepath.Join(dir, name)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if output, err := exec.CommandContext(ctx, "go", "build", "-o", binary, pkg).CombinedOutput(); err != nil {
			t.Fatalf("build %s: %v\n%s", pkg, err, output)
		}
		return binary
	}
	server := build("server", ".")
	write := func(name, data string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	const cue1 = "FILE \"one.bin\" BINARY\r\n  TRACK 01 MODE2/2352\r\n"
	const cue2 = "FILE \"two.bin\" BINARY\n  TRACK 01 MODE2/2352\n"
	write("one.cue", cue1)
	write("one.bin", "first disk")
	write("two.cue", cue2)
	write("two.bin", "second disk")
	manifest := `{
 "platforms":[{"id":"ps","name":"PlayStation","libretro_name":"Sony - PlayStation"}],
 "contents":[
  {"id":"example","canonical_name":"Example Game","display_name":"Пример игры","platform_id":"ps","playlist":"game.m3u","discs":[
   {"entrypoint":"Disc 1/one.cue","files":[{"source":"one.cue","path":"Disc 1/one.cue"},{"source":"one.bin","path":"Disc 1/one.bin"}]},
   {"entrypoint":"Disc 2/two.cue","files":[{"source":"two.cue","path":"Disc 2/two.cue"},{"source":"two.bin","path":"Disc 2/two.bin"}]}]},
  {"id":"other","canonical_name":"Other Game","platform_id":"ps","discs":[
   {"entrypoint":"one.cue","files":[{"source":"one.cue","path":"one.cue"},{"source":"one.bin","path":"one.bin"}]}]}
 ]}`
	write("source.json", manifest)
	write("config.toml", "scan_interval = \"80ms\"\ncleanup_interval = \"80ms\"\ncache_max_age = \"24h\"\n")
	outputDir := filepath.Join(dir, "packages")
	run := func(program string, args ...string) ([]byte, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return exec.CommandContext(ctx, program, args...).CombinedOutput()
	}
	t.Run("rejects invalid catalog before serving", func(t *testing.T) {
		output, err := run(server, "-listen", "127.0.0.1:0", "-catalog", filepath.Join(dir, "missing.json"), "-packages", outputDir)
		if err == nil || !strings.Contains(string(output), "missing.json") {
			t.Fatalf("missing catalog: err=%v output=%s", err, output)
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
	cmd := exec.Command(server, "-config", filepath.Join(dir, "config.toml"), "-listen", address, "-catalog", filepath.Join(dir, "source.json"), "-packages", outputDir)
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
	cacheFiles := func() []string {
		t.Helper()
		files, err := filepath.Glob(filepath.Join(outputDir, ".cache", "*.zip"))
		if err != nil {
			t.Fatal(err)
		}
		return files
	}
	t.Run("catalog and search", func(t *testing.T) {
		all := getCards("")
		if len(all) != 2 || all[0].ID != "example" || all[1].ID != "other" {
			t.Fatalf("catalog: %+v", all)
		}
		for _, query := range []string{"q=EXAMPLE", "q=" + url.QueryEscape("ПРИМЕР")} {
			found := getCards(query)
			if len(found) != 1 || found[0].ID != "example" {
				t.Fatalf("search %s: %+v", query, found)
			}
		}
		if found := getCards("q=absent"); len(found) != 0 {
			t.Fatalf("unexpected matches: %+v", found)
		}
	})
	if files := cacheFiles(); len(files) != 0 {
		t.Fatalf("catalog request created archives: %v", files)
	}
	t.Run("download generated multidisc package", func(t *testing.T) {
		cards := getCards("q=example")
		if len(cards) != 1 {
			t.Fatalf("search returned %d cards", len(cards))
		}
		c := cards[0]
		if c.Entrypoint != "game.m3u" || c.DownloadURL != "packages/example.zip" {
			t.Fatalf("card: %+v", c)
		}
		ref, err := url.Parse(c.DownloadURL)
		if err != nil {
			t.Fatal(err)
		}
		r, err := client.Get(base.ResolveReference(ref).String())
		if err != nil {
			t.Fatal(err)
		}
		defer r.Body.Close()
		if r.StatusCode != http.StatusOK || r.Header.Get("Content-Type") != "application/zip" {
			t.Fatalf("download: status=%d type=%s", r.StatusCode, r.Header.Get("Content-Type"))
		}
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		cached := cacheFiles()
		if len(cached) != 1 {
			t.Fatalf("first download should cache one archive: %v", cached)
		}
		before, err := os.Stat(cached[0])
		if err != nil {
			t.Fatal(err)
		}
		again, err := client.Get(base.ResolveReference(ref).String())
		if err != nil {
			t.Fatal(err)
		}
		againData, err := io.ReadAll(again.Body)
		again.Body.Close()
		if err != nil || again.StatusCode != http.StatusOK || !bytes.Equal(data, againData) {
			t.Fatalf("cached download failed: status=%d err=%v", again.StatusCode, err)
		}
		after, err := os.Stat(cached[0])
		if err != nil || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
			t.Fatalf("repeated download replaced cache: %v", err)
		}
		z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatal(err)
		}
		files := make(map[string]string)
		for _, f := range z.File {
			if _, exists := files[f.Name]; exists {
				t.Fatalf("duplicate archive member %q", f.Name)
			}
			reader, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			contents, err := io.ReadAll(reader)
			if err != nil {
				reader.Close()
				t.Fatal(err)
			}
			if err := reader.Close(); err != nil {
				t.Fatal(err)
			}
			files[f.Name] = string(contents)
		}
		want := map[string]string{"game.m3u": "Disc 1/one.cue\nDisc 2/two.cue\n", "Disc 1/one.cue": cue1, "Disc 1/one.bin": "first disk", "Disc 2/two.cue": cue2, "Disc 2/two.bin": "second disk"}
		if !reflect.DeepEqual(files, want) {
			t.Fatalf("ZIP contents: %#v", files)
		}
		if len(c.Files) != len(want) {
			t.Fatalf("files list: %v", c.Files)
		}
		for _, f := range c.Files {
			if _, ok := want[f]; !ok {
				t.Fatalf("unexpected manifest file %q", f)
			}
			delete(want, f)
		}
		if len(want) != 0 {
			t.Fatalf("missing manifest files: %v", want)
		}
	})
	t.Run("RetroArch content downloader", func(t *testing.T) {
		get := func(path string, status int) []byte {
			t.Helper()
			r, err := client.Get("http://" + address + path)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Body.Close()
			data, err := io.ReadAll(r.Body)
			if err != nil || r.StatusCode != status {
				t.Fatalf("GET %s: status=%d err=%v", path, r.StatusCode, err)
			}
			return data
		}
		dirs := string(get("/assets/cores/.index-dirs", http.StatusOK))
		if dirs != "Sony - PlayStation\n" {
			t.Fatalf("platform index: %q", dirs)
		}
		category := "/assets/cores/" + url.PathEscape("Sony - PlayStation")
		index := string(get(category+"/.index", http.StatusOK))
		if index != "example.zip\nother.zip\n" {
			t.Fatalf("content index: %q", index)
		}
		if got := string(get(category, http.StatusOK)); got != index {
			t.Fatalf("category redirect: %q", got)
		}
		get(category+"/.index-dirs", http.StatusNotFound)
		data := get(category+"/example.zip", http.StatusOK)
		z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatal(err)
		}
		files := make(map[string]string)
		for _, f := range z.File {
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
		want := map[string]string{"example/game.m3u": "Disc 1/one.cue\nDisc 2/two.cue\n", "example/Disc 1/one.cue": cue1, "example/Disc 1/one.bin": "first disk", "example/Disc 2/two.cue": cue2, "example/Disc 2/two.bin": "second disk"}
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
		}{{"GET", "/api/packages/missing.zip", 404}, {"POST", "/api/games", 405}, {"POST", "/api/packages/example.zip", 405}} {
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
	t.Run("manifest changes invalidate cache", func(t *testing.T) {
		changed := strings.ReplaceAll(manifest, "Disc 1/one.cue", "Disc 1/renamed.cue")
		write("source.json", changed)
		cards := getCards("q=example")
		if len(cards) != 1 || !strings.Contains(strings.Join(cards[0].Files, "\n"), "Disc 1/renamed.cue") {
			t.Fatalf("manifest change not reflected in catalog: %+v", cards)
		}
		if files := cacheFiles(); len(files) != 1 {
			t.Fatalf("manifest reload built an archive: %v", files)
		}
		r, err := client.Get("http://" + address + "/api/packages/example.zip")
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil || r.StatusCode != http.StatusOK {
			t.Fatalf("download after manifest change: status=%d err=%v", r.StatusCode, err)
		}
		z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatal(err)
		}
		var playlist string
		for _, f := range z.File {
			if f.Name == "game.m3u" {
				reader, err := f.Open()
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(reader)
				reader.Close()
				if err != nil {
					t.Fatal(err)
				}
				playlist = string(body)
			}
		}
		if playlist != "Disc 1/renamed.cue\nDisc 2/two.cue\n" {
			t.Fatalf("stale playlist after manifest change: %q", playlist)
		}
		if files := cacheFiles(); len(files) != 2 {
			t.Fatalf("expected new cached archive: %v", files)
		}
	})
	t.Run("admin edits manifest", func(t *testing.T) {
		target := "http://" + address + "/api/admin/manifest"
		r, err := client.Get(target)
		if err != nil {
			t.Fatal(err)
		}
		revision := r.Header.Get("ETag")
		data, err := io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil || r.StatusCode != http.StatusOK || revision == "" {
			t.Fatalf("read manifest: status=%d etag=%q err=%v", r.StatusCode, revision, err)
		}
		updated := bytes.Replace(data, []byte("Example Game"), []byte("Renamed Game"), 1)
		save := func(rev string) int {
			t.Helper()
			req, err := http.NewRequest(http.MethodPut, target, bytes.NewReader(updated))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("If-Match", rev)
			req.Header.Set("Origin", "http://"+address)
			response, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			return response.StatusCode
		}
		if status := save(revision); status < 200 || status >= 300 {
			t.Fatalf("save manifest: %d", status)
		}
		if cards := getCards("q=renamed"); len(cards) != 1 || cards[0].ID != "example" {
			t.Fatalf("updated catalog: %+v", cards)
		}
		if status := save(revision); status != http.StatusPreconditionFailed && status != http.StatusConflict {
			t.Fatalf("stale update: %d", status)
		}
	})
	t.Run("scheduled cache cleanup", func(t *testing.T) {
		files := cacheFiles()
		if len(files) < 2 {
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
}
