package catalog

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"retroarch-marketplace/internal/canonicalnames"
	"retroarch-marketplace/internal/content"
	"retroarch-marketplace/internal/store"
)

// Version is the application version string.
var Version = "dev"

type game struct {
	fields                           map[string]json.RawMessage
	id, canonical, display, platform string
	files                            map[string]bool
}

type catalogSnapshot struct {
	revision  int64
	games     []game
	byID      map[string]game
	platforms []content.Platform // every platform, unique by libretro_name
	packages  map[string]content.Item
	assets    map[string]assetRoute
}

// Server serves the catalog and maintains its generated archive cache.
type Server struct {
	store       *store.Store
	finder      canonicalnames.Finder
	packagesDir string
	mu          sync.Mutex
	snapshot    *catalogSnapshot
	scanErr     error
	cacheMu     sync.Mutex
	active      map[string]int
	builds      map[string]*archiveBuild
	writeZIP    func(content.Item, string) error
	assetsMu    sync.Mutex
	assetsFetch *assetsFlight
}

var gameFields = map[string]bool{"id": true, "canonical_name": true, "display_name": true, "platform": true, "entrypoint": true, "files": true, "download_url": true, "boxart_url": true}
var platformFields = map[string]bool{"id": true, "name": true, "libretro_name": true}

// object detects duplicates of protocol fields while retaining unknown members.
func object(raw json.RawMessage, recognized map[string]bool) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	t, err := d.Token()
	if err != nil || t != json.Delim('{') {
		return nil, errors.New("expected object")
	}
	fields := make(map[string]json.RawMessage)
	for d.More() {
		t, err := d.Token()
		if err != nil {
			return nil, err
		}
		key, ok := t.(string)
		if !ok {
			return nil, errors.New("invalid object key")
		}
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return nil, err
		}
		if _, exists := fields[key]; exists && recognized[key] {
			return nil, fmt.Errorf("duplicate field %q", key)
		}
		fields[key] = value
	}
	if _, err := d.Token(); err != nil {
		return nil, err
	}
	return fields, nil
}

func stringField(fields map[string]json.RawMessage, key string, required bool) (string, error) {
	raw, ok := fields[key]
	if !ok && !required {
		return "", nil
	}
	var value string
	if !ok || json.Unmarshal(raw, &value) != nil || value == "" || strings.ContainsRune(value, 0) {
		return "", fmt.Errorf("invalid or missing %s", key)
	}
	return value, nil
}

func safePath(s string, component bool) bool {
	if s == "" || strings.ContainsAny(s, "\\:") || (component && strings.Contains(s, "/")) {
		return false
	}
	for _, b := range []byte(s) {
		if b < 0x20 || b == 0x7f {
			return false
		}
	}
	for _, part := range strings.Split(s, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func parseGame(raw json.RawMessage) (game, error) {
	var g game
	var err error
	g.fields, err = object(raw, gameFields)
	if err != nil {
		return g, err
	}
	if g.id, err = stringField(g.fields, "id", true); err != nil {
		return g, err
	}
	if !safePath(g.id, true) {
		return g, errors.New("unsafe id")
	}
	if g.canonical, err = stringField(g.fields, "canonical_name", true); err != nil {
		return g, err
	}
	if g.display, err = stringField(g.fields, "display_name", false); err != nil {
		return g, err
	}
	for _, key := range []string{"download_url", "boxart_url"} {
		if _, err = stringField(g.fields, key, key == "download_url"); err != nil {
			return g, err
		}
	}
	platform, err := object(g.fields["platform"], platformFields)
	if err != nil {
		return g, fmt.Errorf("platform: %w", err)
	}
	for _, key := range []string{"id", "name", "libretro_name"} {
		value, err := stringField(platform, key, true)
		if err != nil {
			return g, err
		}
		if key == "libretro_name" && !safePath(value, true) {
			return g, errors.New("unsafe libretro_name")
		}
		if key == "libretro_name" {
			g.platform = value
		}
	}
	entry, err := stringField(g.fields, "entrypoint", true)
	if err != nil {
		return g, err
	}
	if !safePath(entry, false) {
		return g, errors.New("unsafe entrypoint")
	}
	var files []string
	if err := json.Unmarshal(g.fields["files"], &files); err != nil || len(files) == 0 {
		return g, errors.New("files must be a nonempty array of strings")
	}
	g.files = make(map[string]bool, len(files))
	for _, file := range files {
		if !safePath(file, false) || g.files[file] {
			return g, fmt.Errorf("unsafe or duplicate file %q", file)
		}
		g.files[file] = true
	}
	if !g.files[entry] {
		return g, errors.New("entrypoint is not in files")
	}
	encoded, _ := json.Marshal("packages/" + url.PathEscape(g.id) + ".zip")
	g.fields["download_url"] = encoded
	return g, nil
}

func (s *Server) load() (*catalogSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ctx := context.Background()
	revision, err := s.store.Revision(ctx)
	if err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	if s.snapshot != nil && s.snapshot.revision == revision {
		return s.snapshot, nil
	}
	items, platforms, err := s.store.AvailableItems(ctx)
	if err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	snapshot := &catalogSnapshot{
		revision:  revision,
		games:     make([]game, 0, len(items)),
		platforms: make([]content.Platform, 0, len(platforms)),
		byID:      make(map[string]game, len(items)),
		packages:  make(map[string]content.Item, len(items)),
	}
	seen := map[string]bool{}
	for _, p := range platforms {
		if !seen[p.LibretroName] {
			seen[p.LibretroName] = true
			snapshot.platforms = append(snapshot.platforms, p)
		}
	}
	for i, item := range items {
		g, err := parseGame(item.Card)
		if err != nil {
			return nil, fmt.Errorf("game %d: %w", i, err)
		}
		snapshot.packages["/api/packages/"+url.PathEscape(g.id)+".zip"] = item
		snapshot.games = append(snapshot.games, g)
		snapshot.byID[g.id] = g
	}
	snapshot.assets = assetRoutes(snapshot.games)
	s.snapshot = snapshot
	return snapshot, nil
}

// New serves the catalog in st, refreshing source availability first.
func New(st *store.Store, packagesDir string) (*Server, error) {
	return NewWithBoxArtDistance(st, packagesDir, nil)
}

// NewWithBoxArtDistance also applies an optional edit distance to BoxArt title lookups.
func NewWithBoxArtDistance(st *store.Store, packagesDir string, maxDistance *int) (*Server, error) {
	s := &Server{store: st, packagesDir: packagesDir, finder: canonicalnames.Finder{Store: st, Client: boxartClient, Now: time.Now, MaxTitleDistance: maxDistance}}
	if err := st.Refresh(context.Background()); err != nil {
		return nil, err
	}
	if _, err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// prepareArchive must be called with cacheMu held.
func (s *Server) prepareArchive(item content.Item) (string, bool, error) {
	if err := os.MkdirAll(s.packagesDir, 0755); err != nil {
		return "", false, err
	}
	info, err := os.Lstat(s.packagesDir)
	if err != nil || !info.IsDir() {
		return "", false, errors.New("package cache root is not a directory")
	}
	dir := filepath.Join(s.packagesDir, ".cache")
	if err := os.Mkdir(dir, 0755); err != nil && !os.IsExist(err) {
		return "", false, err
	}
	info, err = os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return "", false, errors.New("package cache is not a directory")
	}
	idHash := sha256.Sum256([]byte(item.ID))
	path := filepath.Join(dir, fmt.Sprintf("%s-%x.zip", item.Fingerprint, idHash))
	info, err = os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			return "", false, errors.New("cached package is not a regular file")
		}
		return path, true, nil
	}
	if !os.IsNotExist(err) {
		return "", false, err
	}
	return path, false, nil
}

func (s *Server) buildArchive(path string, item content.Item) (string, error) {
	dir := filepath.Dir(path)
	temp, err := os.CreateTemp(dir, ".building-")
	if err != nil {
		return "", err
	}
	tempPath := temp.Name()
	if err := temp.Close(); err != nil {
		os.Remove(tempPath)
		return "", err
	}
	writeZIP := item.WriteZIP
	if s.writeZIP != nil {
		writeZIP = func(path string) error { return s.writeZIP(item, path) }
	}
	if err := writeZIP(tempPath); err != nil {
		os.Remove(tempPath)
		return "", err
	}
	current, err := s.load()
	if err != nil {
		os.Remove(tempPath)
		return "", err
	}
	if latest, ok := current.packages["/api/packages/"+url.PathEscape(item.ID)+".zip"]; !ok || latest.Fingerprint != item.Fingerprint {
		os.Remove(tempPath)
		return "", errors.New("catalog changed while creating package")
	}
	return tempPath, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/admin/") || r.URL.Path == "/admin" || strings.HasPrefix(r.URL.Path, "/admin/") {
		s.serveAdmin(w, r)
		return
	}
	snapshot, err := s.load()
	if err != nil {
		http.Error(w, "catalog unavailable", http.StatusInternalServerError)
		return
	}
	path := r.URL.EscapedPath()
	item, isPackage := snapshot.packages[path]
	asset, isAsset := snapshot.assets[path]
	if path != "/api/games" && path != "/api/platforms" && !isPackage && !isAsset {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if isAsset {
		s.serveRetroArch(w, r, snapshot, asset)
		return
	}
	s.serveCustomAPI(w, r, snapshot, path, item, isPackage)
}
