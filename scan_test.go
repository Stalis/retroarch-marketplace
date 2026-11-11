package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"retroarch-marketplace/internal/store"
)

func TestScan(t *testing.T) {
	status, listing := 200, `<html><body><a href="Tekken%203%20(USA).png">x</a><a href="Tekken%203%20(Europe).png">x</a></body></html>`
	useBoxart(t, &status, &listing)
	root := t.TempDir()
	files := map[string]string{
		"Sony - PlayStation/Tekken3.cue": "FILE \"Tekken3.bin\" BINARY\n",
		"Sony - PlayStation/Tekken3.bin": "tekken",
		"Sony - PlayStation/Foo Bar.bin": "foo",
		"Sony - PlayStation/Broken.cue":  "FILE gone.bin BINARY\n",
		"Sony - PlayStation/.hidden.bin": "hidden",
		"Sony - PlayStation/cover.png":   "image",
		"Unsorted/thing.bin":             "thing",
	}
	for name, data := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db := filepath.Join(root, "catalog.db")
	scan := func(args ...string) (scanResult, int, string) {
		t.Helper()
		var out, errout bytes.Buffer
		code := run(append([]string{"scan", "-database", db}, args...), &out, &errout)
		var result scanResult
		if code == 0 {
			if err := json.Unmarshal(out.Bytes(), &result); err != nil {
				t.Fatalf("output %s: %v", &out, err)
			}
		}
		return result, code, errout.String()
	}
	if _, code, stderr := scan(t.TempDir()); code != 1 || stderr == "" {
		t.Fatalf("outside directory: %d %s", code, stderr)
	}
	if _, code, _ := scan(); code != 2 {
		t.Fatalf("missing directory argument: %d", code)
	}
	dry, code, stderr := scan("-dry-run", root)
	if code != 0 {
		t.Fatalf("dry run: %d %s", code, stderr)
	}
	want := []scanAdded{
		{ID: "tekken-3-usa", CanonicalName: "Tekken 3 (USA)", Platform: "Sony - PlayStation", NameSource: "boxart", Files: []string{"Sony - PlayStation/Tekken3.cue", "Sony - PlayStation/Tekken3.bin"}},
		{ID: "foo-bar", CanonicalName: "Foo Bar", Platform: "Sony - PlayStation", NameSource: "filename", Files: []string{"Sony - PlayStation/Foo Bar.bin"}},
	}
	wantSkipped := []scanSkipped{{"Sony - PlayStation/Broken.cue", "cue reference missing: gone.bin"}, {"Unsorted/thing.bin", "no platform"}}
	if !reflect.DeepEqual(dry.Added, want) || !reflect.DeepEqual(dry.Skipped, wantSkipped) {
		t.Fatalf("dry run: %+v", dry)
	}
	st, err := store.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if games, _ := st.Games(context.Background(), store.Filter{Count: -1}); len(games) != 0 {
		t.Fatalf("dry run wrote games: %v", games)
	}
	if real, code, _ := scan(root); code != 0 || !reflect.DeepEqual(real.Added, want) {
		t.Fatalf("scan: %d %+v", code, real)
	}
	g, _, available, _, err := st.Game(context.Background(), "tekken-3-usa")
	if err != nil || !available || g.PlatformID != "playstation" || g.Discs[0].Entrypoint != "Tekken3.cue" || g.Discs[0].Files[1].Path != "Tekken3.bin" {
		t.Fatalf("stored: %+v %v %v", g, available, err)
	}
}

func TestScanExcludesConfiguredDatabase(t *testing.T) {
	root := t.TempDir()
	alias := filepath.Join(t.TempDir(), "database-dir")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	database := filepath.Join(alias, "catalog.sqlite")
	st, err := store.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, name := range []string{"catalog.sqlite-journal", "other.sqlite", "game.rom"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("content"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, dryRun := range []bool{true, false} {
		result, err := scanDirectory(context.Background(), st, database, root, "Sony - PlayStation", dryRun, true, &bytes.Buffer{})
		if err != nil {
			t.Fatal(err)
		}
		var files []string
		for _, added := range result.Added {
			files = append(files, added.Files...)
		}
		if !reflect.DeepEqual(files, []string{"game.rom", "other.sqlite"}) || len(result.Skipped) != 0 {
			t.Fatalf("dry run %v: %+v", dryRun, result)
		}
	}
}
