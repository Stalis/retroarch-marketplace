// Package store keeps the catalog in a SQLite database.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"modernc.org/sqlite"

	"retroarch-marketplace/internal/content"
)

var (
	ErrNotFound        = errors.New("not found")
	ErrExists          = errors.New("already exists")
	ErrConflict        = errors.New("revision does not match")
	ErrInUse           = errors.New("platform has games")
	ErrUnknownPlatform = errors.New("unknown platform")
)

// MissingError lists sources that do not exist when a write did not allow missing files.
type MissingError struct{ Missing []content.MissingSource }

func (e *MissingError) Error() string {
	return fmt.Sprintf("%d source files are missing", len(e.Missing))
}

func init() {
	sqlite.MustRegisterDeterministicScalarFunction("casefold", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		switch v := args[0].(type) {
		case string:
			return strings.ToLower(v), nil
		case []byte:
			return strings.ToLower(string(v)), nil
		case nil:
			return nil, nil
		default:
			return nil, fmt.Errorf("casefold: unsupported %T", v)
		}
	})
}

type Store struct {
	db   *sql.DB
	root string
}

// Open opens or creates the database; its directory must already exist.
func Open(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dir := filepath.Dir(abs)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("database directory %q does not exist", dir)
	}
	dsn := (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String() +
		"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, root: dir}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("database %q: %w", abs, err)
	}
	return s, nil
}

func (s *Store) migrate() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	switch {
	case version > schemaVersion:
		return fmt.Errorf("database schema version %d is newer than supported %d", version, schemaVersion)
	case version == schemaVersion:
		return nil
	}
	if _, err := tx.Exec(schemaV1); err != nil {
		return err
	}
	if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Close() error { return s.db.Close() }

// Root is the absolute database directory; game sources are relative to it.
func (s *Store) Root() string { return s.root }

func bump(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, "UPDATE meta SET value = value + 1 WHERE key = 'revision'")
	return err
}

// write runs fn in an immediate transaction and bumps the catalog revision.
func (s *Store) write(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	if err := bump(ctx, tx); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Revision(ctx context.Context) (int64, error) {
	var rev int64
	err := s.db.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = 'revision'").Scan(&rev)
	return rev, err
}

type PlatformInfo struct {
	content.Platform
	Games      int `json:"games"`
	TotalGames int `json:"total_games"`
}

func (s *Store) Platforms(ctx context.Context) ([]PlatformInfo, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.id, p.name, p.libretro_name,
		count(g.id) FILTER (WHERE g.available), count(g.id)
		FROM platforms p LEFT JOIN games g ON g.platform_id = p.id GROUP BY p.id ORDER BY p.rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []PlatformInfo{}
	for rows.Next() {
		var p PlatformInfo
		if err := rows.Scan(&p.ID, &p.Name, &p.LibretroName, &p.Games, &p.TotalGames); err != nil {
			return nil, err
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

func exists(ctx context.Context, tx *sql.Tx, query string, args ...any) (bool, error) {
	var found bool
	err := tx.QueryRowContext(ctx, "SELECT EXISTS("+query+")", args...).Scan(&found)
	return found, err
}

func createPlatform(ctx context.Context, tx *sql.Tx, p content.Platform) error {
	if err := content.ValidPlatform(p); err != nil {
		return err
	}
	if found, err := exists(ctx, tx, "SELECT 1 FROM platforms WHERE id = ?", p.ID); err != nil || found {
		if found {
			return fmt.Errorf("platform %q: %w", p.ID, ErrExists)
		}
		return err
	}
	_, err := tx.ExecContext(ctx, "INSERT INTO platforms(id, name, libretro_name) VALUES (?, ?, ?)", p.ID, p.Name, p.LibretroName)
	return err
}

func (s *Store) CreatePlatform(ctx context.Context, p content.Platform) error {
	return s.write(ctx, func(tx *sql.Tx) error { return createPlatform(ctx, tx, p) })
}

func (s *Store) DeletePlatform(ctx context.Context, id string) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		if found, err := exists(ctx, tx, "SELECT 1 FROM platforms WHERE id = ?", id); err != nil || !found {
			if err == nil {
				err = ErrNotFound
			}
			return err
		}
		if found, err := exists(ctx, tx, "SELECT 1 FROM games WHERE platform_id = ?", id); err != nil || found {
			if err == nil {
				err = ErrInUse
			}
			return err
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM platforms WHERE id = ?", id)
		return err
	})
}

// Filter selects games; Count < 0 means unlimited.
type Filter struct {
	Query, PlatformID, LibretroName string
	AvailableOnly                   bool
	Skip, Count                     int
}

type GameSummary struct {
	ID            string  `json:"id"`
	CanonicalName string  `json:"canonical_name"`
	DisplayName   *string `json:"display_name"`
	PlatformID    string  `json:"platform_id"`
	Available     bool    `json:"available"`
	Revision      int64   `json:"revision"`
}

func (f Filter) query(columns string) (string, []any) {
	var sb strings.Builder
	var args []any
	sb.WriteString("SELECT " + columns + " FROM games")
	if f.LibretroName != "" {
		sb.WriteString(" JOIN platforms ON platforms.id = games.platform_id")
	}
	sb.WriteString(" WHERE 1")
	if f.AvailableOnly {
		sb.WriteString(" AND games.available")
	}
	if f.PlatformID != "" {
		sb.WriteString(" AND games.platform_id = ?")
		args = append(args, f.PlatformID)
	}
	if f.LibretroName != "" {
		sb.WriteString(" AND platforms.libretro_name = ?")
		args = append(args, f.LibretroName)
	}
	switch n := utf8.RuneCountInString(f.Query); {
	case n >= 3:
		// A quoted trigram phrase is a case-insensitive substring match.
		sb.WriteString(" AND games.rowid IN (SELECT rowid FROM games_fts WHERE games_fts MATCH ?)")
		args = append(args, `"`+strings.ReplaceAll(f.Query, `"`, `""`)+`"`)
	case n > 0:
		sb.WriteString(" AND (instr(casefold(games.canonical_name), casefold(?)) > 0 OR instr(casefold(coalesce(games.display_name, '')), casefold(?)) > 0)")
		args = append(args, f.Query, f.Query)
	}
	count := f.Count
	if count < 0 {
		count = -1
	}
	sb.WriteString(" ORDER BY games.rowid LIMIT ? OFFSET ?")
	return sb.String(), append(args, count, f.Skip)
}

func (s *Store) Games(ctx context.Context, f Filter) ([]GameSummary, error) {
	q, args := f.query("games.id, games.canonical_name, games.display_name, games.platform_id, games.available, games.revision")
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []GameSummary{}
	for rows.Next() {
		var g GameSummary
		if err := rows.Scan(&g.ID, &g.CanonicalName, &g.DisplayName, &g.PlatformID, &g.Available, &g.Revision); err != nil {
			return nil, err
		}
		result = append(result, g)
	}
	return result, rows.Err()
}

func (s *Store) GameIDs(ctx context.Context, f Filter) ([]string, error) {
	q, args := f.query("games.id")
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		result = append(result, id)
	}
	return result, rows.Err()
}

type stored struct {
	rowid       int64
	game        content.Game
	platform    content.Platform
	revision    int64
	available   bool
	fingerprint string
}

type querier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// loadGames reads the games matching where (a condition on games g) with their discs and files.
func loadGames(ctx context.Context, q querier, where string, args ...any) ([]*stored, error) {
	rows, err := q.QueryContext(ctx, `SELECT g.rowid, g.id, g.canonical_name, g.display_name, g.platform_id, g.playlist, g.revision, g.available, g.fingerprint,
		p.name, p.libretro_name FROM games g JOIN platforms p ON p.id = g.platform_id WHERE `+where+` ORDER BY g.rowid`, args...)
	if err != nil {
		return nil, err
	}
	var games []*stored
	byRow := map[int64]*stored{}
	for rows.Next() {
		g := &stored{}
		if err := rows.Scan(&g.rowid, &g.game.ID, &g.game.CanonicalName, &g.game.DisplayName, &g.game.PlatformID, &g.game.Playlist, &g.revision, &g.available, &g.fingerprint,
			&g.platform.Name, &g.platform.LibretroName); err != nil {
			rows.Close()
			return nil, err
		}
		g.platform.ID = g.game.PlatformID
		games = append(games, g)
		byRow[g.rowid] = g
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(games) == 0 {
		return games, err
	}
	sub := "SELECT g.rowid FROM games g JOIN platforms p ON p.id = g.platform_id WHERE " + where
	rows, err = q.QueryContext(ctx, "SELECT game_rowid, entrypoint FROM discs WHERE game_rowid IN ("+sub+") ORDER BY game_rowid, disc", args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var row int64
		var entry string
		if err := rows.Scan(&row, &entry); err != nil {
			rows.Close()
			return nil, err
		}
		if g := byRow[row]; g != nil {
			g.game.Discs = append(g.game.Discs, content.Disc{Entrypoint: entry})
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = q.QueryContext(ctx, "SELECT game_rowid, disc, source, path FROM files WHERE game_rowid IN ("+sub+") ORDER BY game_rowid, disc, position", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var row int64
		var disc int
		var f content.File
		if err := rows.Scan(&row, &disc, &f.Source, &f.Path); err != nil {
			return nil, err
		}
		if g := byRow[row]; g != nil && disc < len(g.game.Discs) {
			g.game.Discs[disc].Files = append(g.game.Discs[disc].Files, f)
		}
	}
	return games, rows.Err()
}

// inspect stats every source; err reports unusable sources other than missing ones.
func (s *Store) inspect(g content.Game, p content.Platform) (available bool, fingerprint string, missing []content.MissingSource, err error) {
	item, err := content.NewItem(g, p, s.root)
	if err != nil {
		return false, "", nil, err
	}
	hash := sha256.New()
	fmt.Fprintf(hash, "%q:%q;", item.Card, item.Playlist())
	sources := item.Sources()
	i := 0
	for _, disc := range g.Discs {
		for _, f := range disc.Files {
			path := sources[i]
			i++
			info, err := os.Stat(path)
			switch {
			case errors.Is(err, fs.ErrNotExist):
				missing = append(missing, content.MissingSource{ID: g.ID, Path: f.Source})
				fmt.Fprintf(hash, "%q:missing;", path)
			case err != nil:
				return false, "", nil, fmt.Errorf("source %q: %w", path, err)
			case !info.Mode().IsRegular():
				return false, "", nil, fmt.Errorf("source %q is not a regular file", path)
			default:
				fmt.Fprintf(hash, "%q:%d:%d;", path, info.Size(), info.ModTime().UnixNano())
			}
		}
	}
	return len(missing) == 0, fmt.Sprintf("%x", hash.Sum(nil)), missing, nil
}

func (s *Store) Game(ctx context.Context, id string) (content.Game, int64, bool, []content.MissingSource, error) {
	games, err := loadGames(ctx, s.db, "g.id = ?", id)
	if err != nil {
		return content.Game{}, 0, false, nil, err
	}
	if len(games) == 0 {
		return content.Game{}, 0, false, nil, ErrNotFound
	}
	g := games[0]
	_, _, missing, err := s.inspect(g.game, g.platform)
	if err != nil {
		// Unusable sources are reported by Refresh; the stored definition is still editable.
		missing = nil
	}
	if missing == nil {
		missing = []content.MissingSource{}
	}
	return g.game, g.revision, g.available, missing, nil
}

// prepare validates g against the transaction state and inspects its sources.
func (s *Store) prepare(ctx context.Context, tx *sql.Tx, g content.Game, allowMissing bool) (bool, string, error) {
	if err := g.Validate(); err != nil {
		return false, "", err
	}
	var p content.Platform
	err := tx.QueryRowContext(ctx, "SELECT id, name, libretro_name FROM platforms WHERE id = ?", g.PlatformID).Scan(&p.ID, &p.Name, &p.LibretroName)
	if errors.Is(err, sql.ErrNoRows) {
		return false, "", fmt.Errorf("%w %q", ErrUnknownPlatform, g.PlatformID)
	}
	if err != nil {
		return false, "", err
	}
	available, fingerprint, missing, err := s.inspect(g, p)
	if err != nil {
		return false, "", err
	}
	if len(missing) > 0 && !allowMissing {
		return false, "", &MissingError{missing}
	}
	return available, fingerprint, nil
}

func insertFiles(ctx context.Context, tx *sql.Tx, rowid int64, g content.Game) error {
	for i, disc := range g.Discs {
		if _, err := tx.ExecContext(ctx, "INSERT INTO discs(game_rowid, disc, entrypoint) VALUES (?, ?, ?)", rowid, i, disc.Entrypoint); err != nil {
			return err
		}
		for j, f := range disc.Files {
			if _, err := tx.ExecContext(ctx, "INSERT INTO files(game_rowid, disc, position, source, path) VALUES (?, ?, ?, ?, ?)", rowid, i, j, f.Source, f.Path); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) createGame(ctx context.Context, tx *sql.Tx, g content.Game, allowMissing bool) error {
	if found, err := exists(ctx, tx, "SELECT 1 FROM games WHERE id = ?", g.ID); err != nil || found {
		if found {
			return fmt.Errorf("game %q: %w", g.ID, ErrExists)
		}
		return err
	}
	available, fingerprint, err := s.prepare(ctx, tx, g, allowMissing)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, "INSERT INTO games(id, canonical_name, display_name, platform_id, playlist, available, fingerprint) VALUES (?, ?, ?, ?, ?, ?, ?)",
		g.ID, g.CanonicalName, g.DisplayName, g.PlatformID, g.Playlist, available, fingerprint)
	if err != nil {
		return err
	}
	rowid, err := res.LastInsertId()
	if err != nil {
		return err
	}
	return insertFiles(ctx, tx, rowid, g)
}

// CreateGame stores a new game and returns its revision.
func (s *Store) CreateGame(ctx context.Context, g content.Game, allowMissing bool) (int64, error) {
	return 1, s.write(ctx, func(tx *sql.Tx) error { return s.createGame(ctx, tx, g, allowMissing) })
}

// CreateGames adds absent platforms and then games in one transaction; missing sources are rejected.
func (s *Store) CreateGames(ctx context.Context, games []content.Game, platforms []content.Platform) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		for _, p := range platforms {
			if err := createPlatform(ctx, tx, p); err != nil && !errors.Is(err, ErrExists) {
				return err
			}
		}
		for _, g := range games {
			if err := s.createGame(ctx, tx, g, false); err != nil {
				return err
			}
		}
		return nil
	})
}

// revisionError distinguishes an absent game from a stale revision after a guarded write matched nothing.
func revisionError(ctx context.Context, tx *sql.Tx, id string) error {
	found, err := exists(ctx, tx, "SELECT 1 FROM games WHERE id = ?", id)
	if err != nil {
		return err
	}
	if found {
		return ErrConflict
	}
	return ErrNotFound
}

// UpdateGame replaces game g if its stored revision matches and returns the new revision.
func (s *Store) UpdateGame(ctx context.Context, g content.Game, revision int64, allowMissing bool) (int64, error) {
	err := s.write(ctx, func(tx *sql.Tx) error {
		if err := g.Validate(); err != nil {
			return err
		}
		var rowid int64
		err := tx.QueryRowContext(ctx, "SELECT rowid FROM games WHERE id = ? AND revision = ?", g.ID, revision).Scan(&rowid)
		if errors.Is(err, sql.ErrNoRows) {
			return revisionError(ctx, tx, g.ID)
		}
		if err != nil {
			return err
		}
		available, fingerprint, err := s.prepare(ctx, tx, g, allowMissing)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE games SET canonical_name = ?, display_name = ?, platform_id = ?, playlist = ?, available = ?, fingerprint = ?, revision = revision + 1
			WHERE rowid = ?`, g.CanonicalName, g.DisplayName, g.PlatformID, g.Playlist, available, fingerprint, rowid); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM files WHERE game_rowid = ?", rowid); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM discs WHERE game_rowid = ?", rowid); err != nil {
			return err
		}
		return insertFiles(ctx, tx, rowid, g)
	})
	return revision + 1, err
}

func (s *Store) DeleteGame(ctx context.Context, id string, revision int64) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "DELETE FROM games WHERE id = ? AND revision = ?", id, revision)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n == 0 {
			if err == nil {
				err = revisionError(ctx, tx, id)
			}
			return err
		}
		return nil
	})
}

// HasSource reports whether any game uses source (a slash path relative to Root).
func (s *Store) HasSource(ctx context.Context, source string) (bool, error) {
	var found bool
	err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM files WHERE source = ?)", source).Scan(&found)
	return found, err
}

// Refresh re-stats every source and records availability and fingerprints that changed.
func (s *Store) Refresh(ctx context.Context) error {
	games, err := loadGames(ctx, s.db, "1")
	if err != nil {
		return err
	}
	type change struct {
		rowid, revision int64
		available       bool
		fingerprint     string
	}
	var changes []change
	for _, g := range games {
		available, fingerprint, _, err := s.inspect(g.game, g.platform)
		if err != nil {
			log.Printf("%v", err)
			available, fingerprint = false, ""
		}
		if available != g.available || fingerprint != g.fingerprint {
			changes = append(changes, change{g.rowid, g.revision, available, fingerprint})
		}
	}
	if len(changes) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	changed := false
	for _, c := range changes {
		// The revision guard skips games edited since they were read; that write already set fresh values.
		res, err := tx.ExecContext(ctx, "UPDATE games SET available = ?, fingerprint = ? WHERE rowid = ? AND revision = ?", c.available, c.fingerprint, c.rowid, c.revision)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n > 0 {
			changed = true
		}
	}
	if changed {
		if err := bump(ctx, tx); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// AvailableItems returns the available games as packages and every platform, both in insertion order.
func (s *Store) AvailableItems(ctx context.Context) ([]content.Item, []content.Platform, error) {
	games, err := loadGames(ctx, s.db, "g.available")
	if err != nil {
		return nil, nil, err
	}
	items := make([]content.Item, 0, len(games))
	for _, g := range games {
		item, err := content.NewItem(g.game, g.platform, s.root)
		if err != nil {
			return nil, nil, fmt.Errorf("game %q: %w", g.game.ID, err)
		}
		item.Fingerprint = g.fingerprint
		items = append(items, item)
	}
	platforms, err := s.Platforms(ctx)
	if err != nil {
		return nil, nil, err
	}
	result := make([]content.Platform, len(platforms))
	for i, p := range platforms {
		result[i] = p.Platform
	}
	return items, result, nil
}
