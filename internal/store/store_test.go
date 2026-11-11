package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"retroarch-marketplace/internal/content"
)

var psx = content.Platform{ID: "psx", Name: "PlayStation", LibretroName: "Sony - PlayStation"}

func open(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func game(id, name, source string) content.Game {
	return content.Game{ID: id, CanonicalName: name, PlatformID: psx.ID, Discs: []content.Disc{{Entrypoint: "game.bin", Files: []content.File{{Source: source, Path: "game.bin"}}}}}
}

func write(t *testing.T, st *Store, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(st.Root(), name), []byte(name), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestSearch(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	if err := st.CreatePlatform(ctx, psx); err != nil {
		t.Fatal(err)
	}
	write(t, st, "a.bin")
	write(t, st, "b.bin")
	display := "Étoile"
	b := game("b", "Another Game", "b.bin")
	b.DisplayName = &display
	for _, g := range []content.Game{game("a", "Хроно Триггер", "a.bin"), b} {
		if _, err := st.CreateGame(ctx, g, false); err != nil {
			t.Fatal(err)
		}
	}
	for q, want := range map[string][]string{
		"": {"a", "b"}, "триг": {"a"}, "ТР": {"a"}, "zz": {}, "éTOI": {"b"}, "Ét": {"b"}, `"x`: {}, "game": {"b"},
	} {
		ids, err := st.GameIDs(ctx, Filter{Query: q, AvailableOnly: true, Count: -1})
		if err != nil || !reflect.DeepEqual(ids, want) {
			t.Errorf("q=%q: %v %v", q, ids, err)
		}
	}
	ids, err := st.GameIDs(ctx, Filter{LibretroName: psx.LibretroName, Skip: 1, Count: 5})
	if err != nil || !reflect.DeepEqual(ids, []string{"b"}) {
		t.Fatalf("paged: %v %v", ids, err)
	}
	if ids, _ := st.GameIDs(ctx, Filter{LibretroName: "Other", Count: -1}); len(ids) != 0 {
		t.Fatalf("platform filter: %v", ids)
	}
}

func TestRevisionsAndAvailability(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	if err := st.CreatePlatform(ctx, psx); err != nil {
		t.Fatal(err)
	}
	var missing *MissingError
	if _, err := st.CreateGame(ctx, game("g", "G", "g.bin"), false); !errors.As(err, &missing) || len(missing.Missing) != 1 {
		t.Fatalf("missing source accepted: %v", err)
	}
	rev, err := st.CreateGame(ctx, game("g", "G", "g.bin"), true)
	if err != nil || rev != 1 {
		t.Fatal(rev, err)
	}
	if _, err := st.CreateGame(ctx, game("g", "G", "g.bin"), true); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate: %v", err)
	}
	if err := st.DeletePlatform(ctx, psx.ID); !errors.Is(err, ErrInUse) {
		t.Fatalf("delete used platform: %v", err)
	}
	items, _, _ := st.AvailableItems(ctx)
	if len(items) != 0 {
		t.Fatal("unavailable game listed")
	}
	before, _ := st.Revision(ctx)
	if err := st.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if after, _ := st.Revision(ctx); after != before {
		t.Fatalf("idle refresh changed revision %d -> %d", before, after)
	}
	write(t, st, "g.bin")
	if err := st.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if after, _ := st.Revision(ctx); after == before {
		t.Fatal("refresh did not record availability")
	}
	if items, _, _ := st.AvailableItems(ctx); len(items) != 1 || items[0].Fingerprint == "" {
		t.Fatalf("items: %+v", items)
	}
	updated := game("g", "G2", "g.bin")
	if _, err := st.UpdateGame(ctx, updated, 1, false); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateGame(ctx, updated, 1, false); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update: %v", err)
	}
	if _, err := st.UpdateGame(ctx, game("x", "X", "g.bin"), 1, false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("absent update: %v", err)
	}
	g, rev, available, miss, err := st.Game(ctx, "g")
	if err != nil || g.CanonicalName != "G2" || rev != 2 || !available || len(miss) != 0 {
		t.Fatalf("game: %+v %d %v %v %v", g, rev, available, miss, err)
	}
	if err := st.DeleteGame(ctx, "g", 1); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale delete: %v", err)
	}
	if err := st.DeleteGame(ctx, "g", 2); err != nil {
		t.Fatal(err)
	}
	if found, _ := st.HasSource(ctx, "g.bin"); found {
		t.Fatal("files not cascaded")
	}
	if err := st.DeletePlatform(ctx, psx.ID); err != nil {
		t.Fatal(err)
	}
}

func TestOpenRejectsNewerSchemaAndMissingDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 2"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := Open(path); err == nil {
		t.Fatal("opened a newer schema")
	}
	if _, err := Open(filepath.Join(t.TempDir(), "missing-dir", "catalog.db")); err == nil {
		t.Fatal("opened in a missing directory")
	}
}

func TestBoxartCache(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	now := time.Unix(1_000_000, 0)
	if err := st.ReplaceBoxart(ctx, "P", []string{"Tekken 3 (USA)", "Pokémon Snap (USA)"}, now); err != nil {
		t.Fatal(err)
	}
	if names, ok, err := st.BoxartNames(ctx, "P", now); err != nil || !ok || len(names) != 2 {
		t.Fatal(names, ok, err)
	}
	if _, ok, _ := st.BoxartNames(ctx, "P", now.Add(time.Second)); ok {
		t.Fatal("stale listing served")
	}
	if names, err := st.BoxartCandidates(ctx, "P", `"pok" OR "kem"`, 10); err != nil || len(names) != 1 || names[0] != "Pokémon Snap (USA)" {
		t.Fatal(names, err)
	}
	if n, err := st.PurgeBoxart(ctx, now.Add(time.Second)); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if names, err := st.BoxartCandidates(ctx, "P", `"tek"`, 10); err != nil || len(names) != 0 {
		t.Fatal(names, err)
	}
}
