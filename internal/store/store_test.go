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
	if _, err := db.Exec("PRAGMA user_version = 3"); err != nil {
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
	if n, err := st.PurgeBoxart(ctx, now.Add(time.Second)); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if _, ok, err := st.BoxartNames(ctx, "P", now); err != nil || ok {
		t.Fatal(ok, err)
	}
}

func TestRecreatedGameRejectsOldRevision(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	if err := st.CreatePlatform(ctx, psx); err != nil {
		t.Fatal(err)
	}
	g := game("g", "Game", "g.bin")
	old, err := st.CreateGame(ctx, g, true)
	if err != nil {
		t.Fatal(err)
	}
	old, err = st.UpdateGame(ctx, g, old, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteGame(ctx, g.ID, old); err != nil {
		t.Fatal(err)
	}
	current, err := st.CreateGame(ctx, g, true)
	if err != nil || current <= old {
		t.Fatalf("recreated revision = %d, old = %d, error = %v", current, old, err)
	}
	if _, err := st.UpdateGame(ctx, g, old, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("old update: %v", err)
	}
	if err := st.DeleteGame(ctx, g.ID, old); !errors.Is(err, ErrConflict) {
		t.Fatalf("old delete: %v", err)
	}
}

func TestRefreshSkipsReusedRow(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	if err := st.CreatePlatform(ctx, psx); err != nil {
		t.Fatal(err)
	}
	old, err := st.CreateGame(ctx, game("old", "Old", "missing"), true)
	if err != nil {
		t.Fatal(err)
	}
	var row int64
	if err := st.db.QueryRow("SELECT rowid FROM games WHERE id = 'old'").Scan(&row); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteGame(ctx, "old", old); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateGame(ctx, game("new", "New", "missing"), true); err != nil {
		t.Fatal(err)
	}
	var newRow int64
	if err := st.db.QueryRow("SELECT rowid FROM games WHERE id = 'new'").Scan(&newRow); err != nil || newRow != row {
		t.Fatalf("expected reused row %d, got %d: %v", row, newRow, err)
	}
	before, err := st.Revision(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var initialAvailable bool
	var initialFingerprint string
	if err := st.db.QueryRow("SELECT available, fingerprint FROM games WHERE id = 'new'").Scan(&initialAvailable, &initialFingerprint); err != nil {
		t.Fatal(err)
	}
	if err := st.applyRefresh(ctx, []refreshChange{{row, old, "old", true, "stale"}}); err != nil {
		t.Fatal(err)
	}
	var available bool
	var fingerprint string
	if err := st.db.QueryRow("SELECT available, fingerprint FROM games WHERE id = 'new'").Scan(&available, &fingerprint); err != nil || available != initialAvailable || fingerprint != initialFingerprint {
		t.Fatalf("new game overwritten: %v %q %v", available, fingerprint, err)
	}
	if after, err := st.Revision(ctx); err != nil || after != before {
		t.Fatalf("revision changed: %d -> %d: %v", before, after, err)
	}
}

func TestCreateGamesRejectsConflictingPlatforms(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	if err := st.CreatePlatform(ctx, psx); err != nil {
		t.Fatal(err)
	}
	write(t, st, "g.bin")
	conflict := psx
	conflict.Name = "Different"
	if err := st.CreateGames(ctx, []content.Game{game("g", "Game", "g.bin")}, []content.Platform{conflict}); !errors.Is(err, ErrExists) {
		t.Fatalf("conflicting platform: %v", err)
	}
	if _, _, _, _, err := st.Game(ctx, "g"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("game written despite conflict: %v", err)
	}
	other := content.Platform{ID: "other", Name: "Other", LibretroName: "Other"}
	otherConflict := other
	otherConflict.LibretroName = "Different"
	if err := st.CreateGames(ctx, nil, []content.Platform{other, otherConflict}); !errors.Is(err, ErrExists) {
		t.Fatalf("conflicting batch: %v", err)
	}
	platforms, err := st.Platforms(ctx)
	if err != nil || len(platforms) != 1 {
		t.Fatalf("batch was not rolled back: %v %v", platforms, err)
	}
	if err := st.CreateGames(ctx, []content.Game{game("g", "Game", "g.bin")}, []content.Platform{psx, psx}); err != nil {
		t.Fatalf("identical platform: %v", err)
	}
}

func TestSearchSimpleFold(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	if err := st.CreatePlatform(ctx, psx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateGame(ctx, game("g", "ΟΣ", "missing"), true); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"σ", "ς", "Σ", "ος", "οσ"} {
		if ids, err := st.GameIDs(ctx, Filter{Query: q, Count: -1}); err != nil || !reflect.DeepEqual(ids, []string{"g"}) {
			t.Errorf("search %q: %v %v", q, ids, err)
		}
	}
}

func TestMigrateV1PreservesGames(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "catalog.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(schemaV1 + `INSERT INTO platforms(id, name, libretro_name) VALUES ('psx', 'PlayStation', 'Sony - PlayStation');
		INSERT INTO games(id, canonical_name, platform_id, revision) VALUES ('g', 'Game', 'psx', 7);
		UPDATE meta SET value = 10 WHERE key = 'revision';
		PRAGMA user_version = 1;`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.DeleteGame(ctx, "g", 7); !errors.Is(err, ErrConflict) {
		t.Fatalf("pre-migration revision accepted: %v", err)
	}
	if err := st.DeleteGame(ctx, "g", 11); err != nil {
		t.Fatal(err)
	}
	if revision, err := st.CreateGame(ctx, game("g", "Game", "missing"), true); err != nil || revision != 12 {
		t.Fatalf("migrated revision = %d: %v", revision, err)
	}
	if revision, err := st.CreateGame(ctx, game("deleted-before-upgrade", "Old", "missing"), true); err != nil || revision <= 10 {
		t.Fatalf("pre-upgrade deleted ID revision = %d: %v", revision, err)
	}
}

func TestGameReadsConsistentDefinitionDuringWrites(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	if err := st.CreatePlatform(ctx, psx); err != nil {
		t.Fatal(err)
	}
	g := game("g", "a", "a.bin")
	revision, err := st.CreateGame(ctx, g, true)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		for i := range 80 {
			name := "a"
			if i%2 == 0 {
				name = "b"
			}
			g := game("g", name, name+".bin")
			var err error
			revision, err = st.UpdateGame(ctx, g, revision, true)
			if err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	for range 80 {
		got, _, _, _, err := st.Game(ctx, "g")
		if err != nil {
			t.Error(err)
			break
		}
		if len(got.Discs) != 1 || len(got.Discs[0].Files) != 1 || got.Discs[0].Files[0].Source != got.CanonicalName+".bin" {
			t.Errorf("inconsistent game: %+v", got)
			break
		}
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
