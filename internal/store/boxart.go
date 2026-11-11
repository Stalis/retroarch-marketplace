package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Boxart listings are a lookup cache, not catalog data, so they never bump the catalog revision.

// BoxartNames returns the cached listing of platform when it was fetched at or after fresh.
func (s *Store) BoxartNames(ctx context.Context, platform string, fresh time.Time) ([]string, bool, error) {
	var fetched int64
	err := s.db.QueryRowContext(ctx, "SELECT fetched_at FROM boxart_listings WHERE platform = ?", platform).Scan(&fetched)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && fetched < fresh.Unix()) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT name FROM boxart_fts WHERE platform = ? ORDER BY rowid", platform)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, false, err
		}
		names = append(names, name)
	}
	return names, true, rows.Err()
}

// BoxartCandidates returns cached names of platform matching an FTS5 query, best first.
func (s *Store) BoxartCandidates(ctx context.Context, platform, ftsQuery string, limit int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT name FROM boxart_fts WHERE boxart_fts MATCH ? AND platform = ? ORDER BY bm25(boxart_fts) LIMIT ?", ftsQuery, platform, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func (s *Store) ReplaceBoxart(ctx context.Context, platform string, names []string, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "DELETE FROM boxart_fts WHERE platform = ?", platform); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, "INSERT INTO boxart_fts(title, platform, name) VALUES (?, ?, ?)")
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, name := range names {
		if _, err := stmt.ExecContext(ctx, name, platform, name); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO boxart_listings(platform, fetched_at) VALUES (?, ?) ON CONFLICT(platform) DO UPDATE SET fetched_at = excluded.fetched_at", platform, now.Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

// PurgeBoxart deletes listings fetched before the cutoff and returns how many were removed.
func (s *Store) PurgeBoxart(ctx context.Context, before time.Time) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	cutoff := before.Unix()
	if _, err := tx.ExecContext(ctx, "DELETE FROM boxart_fts WHERE platform IN (SELECT platform FROM boxart_listings WHERE fetched_at < ?)", cutoff); err != nil {
		return 0, err
	}
	res, err := tx.ExecContext(ctx, "DELETE FROM boxart_listings WHERE fetched_at < ?", cutoff)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(n), tx.Commit()
}
