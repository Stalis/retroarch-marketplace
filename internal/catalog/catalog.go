package catalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"retroarch-marketplace/internal/content"
)

type game struct {
	fields                           map[string]json.RawMessage
	id, canonical, display, platform string
	files                            map[string]bool
}

type catalogSnapshot struct {
	fingerprint string
	games       []game
	packages    map[string]content.Item
	assets      map[string]assetRoute
}

// Server serves the catalog and maintains its generated archive cache.
type Server struct {
	catalogPath, packagesDir string
	mu                       sync.Mutex
	snapshot                 *catalogSnapshot
	scanErr                  error
	cacheMu                  sync.Mutex
	active                   map[string]int
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
	// shortcut: same-size edits preserving mtime are invisible; hash file contents if that must be detected.
	items, fingerprint, err := content.Load(s.catalogPath)
	if err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	if s.snapshot != nil && s.snapshot.fingerprint == fingerprint {
		return s.snapshot, nil
	}
	snapshot := &catalogSnapshot{
		fingerprint: fingerprint,
		games:       make([]game, 0, len(items)),
		packages:    make(map[string]content.Item, len(items)),
	}
	for i, item := range items {
		if !item.Available {
			continue
		}
		g, err := parseGame(item.Card)
		if err != nil {
			return nil, fmt.Errorf("game %d: %w", i, err)
		}
		snapshot.packages["/api/packages/"+url.PathEscape(g.id)+".zip"] = item
		snapshot.games = append(snapshot.games, g)
	}
	snapshot.assets = assetRoutes(snapshot.games)
	s.snapshot = snapshot
	return snapshot, nil
}

func New(catalogPath, packagesDir string) (*Server, error) {
	s := &Server{catalogPath: catalogPath, packagesDir: packagesDir}
	if _, err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// archiveLocked must be called with cacheMu held.
func (s *Server) archiveLocked(fingerprint string, item content.Item) (string, error) {
	if err := os.MkdirAll(s.packagesDir, 0755); err != nil {
		return "", err
	}
	info, err := os.Lstat(s.packagesDir)
	if err != nil || !info.IsDir() {
		return "", errors.New("package cache root is not a directory")
	}
	dir := filepath.Join(s.packagesDir, ".cache")
	if err := os.Mkdir(dir, 0755); err != nil && !os.IsExist(err) {
		return "", err
	}
	info, err = os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return "", errors.New("package cache is not a directory")
	}
	idHash := sha256.Sum256([]byte(item.ID))
	path := filepath.Join(dir, fmt.Sprintf("%s-%x.zip", fingerprint, idHash))
	info, err = os.Lstat(path)
	if err == nil {
		if !info.Mode().IsRegular() {
			return "", errors.New("cached package is not a regular file")
		}
		return path, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	temp, err := os.CreateTemp(dir, ".building-")
	if err != nil {
		return "", err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Close(); err != nil {
		return "", err
	}
	if err := item.WriteZIP(tempPath); err != nil {
		return "", err
	}
	current, err := s.load()
	if err != nil {
		return "", err
	}
	if current.fingerprint != fingerprint {
		return "", errors.New("catalog changed while creating package")
	}
	if err := os.Rename(tempPath, path); err != nil {
		return "", err
	}
	return path, nil
}

func foldEqual(a, b rune) bool {
	if a == b {
		return true
	}
	for r := unicode.SimpleFold(a); r != a; r = unicode.SimpleFold(r) {
		if r == b {
			return true
		}
	}
	return false
}

func containsFold(s, query string) bool {
	if query == "" {
		return true
	}
	needle := []rune(query)
	for i := range s {
		rest := s[i:]
		matched := true
		for _, want := range needle {
			if rest == "" {
				matched = false
				break
			}
			got, size := utf8.DecodeRuneInString(rest)
			if !foldEqual(got, want) {
				matched = false
				break
			}
			rest = rest[size:]
		}
		if matched {
			return true
		}
	}
	return false
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api/admin/manifest" || r.URL.Path == "/admin" || strings.HasPrefix(r.URL.Path, "/admin/") {
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
	if path != "/api/games" && !isPackage && !isAsset {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if isAsset {
		if !asset.isIndex && asset.redirect == "" {
			item = snapshot.packages["/api/packages/"+url.PathEscape(asset.archive)+".zip"]
			asset.archive, err = s.archive(snapshot.fingerprint, item)
			if err != nil {
				http.Error(w, "package unavailable", http.StatusInternalServerError)
				return
			}
			defer s.releaseArchive(asset.archive)
		}
		asset.serve(w, r)
		return
	}
	if isPackage {
		packagePath, err := s.archive(snapshot.fingerprint, item)
		if err != nil {
			http.Error(w, "package unavailable", http.StatusInternalServerError)
			return
		}
		defer s.releaseArchive(packagePath)
		file, err := os.Open(packagePath)
		if err != nil {
			http.Error(w, "package unavailable", http.StatusInternalServerError)
			return
		}
		defer file.Close()
		w.Header().Set("Content-Type", "application/zip")
		_, _ = io.Copy(w, file)
		return
	}
	params, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		http.Error(w, "invalid query", http.StatusBadRequest)
		return
	}
	query := params.Get("q")
	results := make([]map[string]json.RawMessage, 0)
	for _, g := range snapshot.games {
		if containsFold(g.canonical, query) || containsFold(g.display, query) {
			results = append(results, g.fields)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(results)
}
