package store

const schemaVersion = 1

const schemaV1 = `
CREATE TABLE meta(key TEXT PRIMARY KEY, value INTEGER NOT NULL) STRICT;
INSERT INTO meta VALUES('revision', 0);
CREATE TABLE platforms(rowid INTEGER PRIMARY KEY, id TEXT NOT NULL UNIQUE, name TEXT NOT NULL, libretro_name TEXT NOT NULL) STRICT;
CREATE TABLE games(rowid INTEGER PRIMARY KEY, id TEXT NOT NULL UNIQUE, canonical_name TEXT NOT NULL, display_name TEXT,
  platform_id TEXT NOT NULL REFERENCES platforms(id), playlist TEXT, revision INTEGER NOT NULL DEFAULT 1,
  available INTEGER NOT NULL DEFAULT 0, fingerprint TEXT NOT NULL DEFAULT '') STRICT;
CREATE INDEX games_platform ON games(platform_id, available);
CREATE TABLE discs(game_rowid INTEGER NOT NULL REFERENCES games(rowid) ON DELETE CASCADE, disc INTEGER NOT NULL, entrypoint TEXT NOT NULL, PRIMARY KEY(game_rowid, disc)) STRICT;
CREATE TABLE files(game_rowid INTEGER NOT NULL REFERENCES games(rowid) ON DELETE CASCADE, disc INTEGER NOT NULL, position INTEGER NOT NULL, source TEXT NOT NULL, path TEXT NOT NULL, PRIMARY KEY(game_rowid, disc, position)) STRICT;
CREATE INDEX files_source ON files(source);
CREATE VIRTUAL TABLE games_fts USING fts5(canonical_name, display_name, content='games', content_rowid='rowid', tokenize='trigram');
CREATE TRIGGER games_ai AFTER INSERT ON games BEGIN
  INSERT INTO games_fts(rowid, canonical_name, display_name) VALUES (new.rowid, new.canonical_name, new.display_name);
END;
CREATE TRIGGER games_ad AFTER DELETE ON games BEGIN
  INSERT INTO games_fts(games_fts, rowid, canonical_name, display_name) VALUES ('delete', old.rowid, old.canonical_name, old.display_name);
END;
CREATE TRIGGER games_au AFTER UPDATE OF canonical_name, display_name ON games BEGIN
  INSERT INTO games_fts(games_fts, rowid, canonical_name, display_name) VALUES ('delete', old.rowid, old.canonical_name, old.display_name);
  INSERT INTO games_fts(rowid, canonical_name, display_name) VALUES (new.rowid, new.canonical_name, new.display_name);
END;
CREATE TABLE boxart_listings(platform TEXT PRIMARY KEY, fetched_at INTEGER NOT NULL) STRICT;
CREATE VIRTUAL TABLE boxart_fts USING fts5(title, platform UNINDEXED, name UNINDEXED, tokenize='trigram remove_diacritics 1');
`
