package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"retroarch-marketplace/internal/content"
	"retroarch-marketplace/internal/store"
)

const maxAdminBody = 1 << 20

func localAdmin(r *http.Request) bool {
	remote, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || net.ParseIP(remote) == nil || !net.ParseIP(remote).IsLoopback() {
		return false
	}
	u, err := url.Parse("http://" + r.Host)
	if err != nil || u.Host != r.Host || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	return strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())
}

func (s *Server) serveAdmin(w http.ResponseWriter, r *http.Request) {
	if Version != "" {
		w.Header().Set("X-Catalog-Version", Version)
	}
	if !localAdmin(r) {
		http.Error(w, "admin access requires a loopback client and host", http.StatusForbidden)
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/api/admin/") {
		serveAdminFiles(w, r)
		return
	}
	if r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodDelete {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		if r.Header.Get("Origin") != scheme+"://"+r.Host {
			http.Error(w, "origin must match the request host", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodDelete {
			mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || mediaType != "application/json" {
				http.Error(w, "Content-Type must be application/json", http.StatusBadRequest)
				return
			}
		}
	}
	rest := strings.TrimPrefix(r.URL.EscapedPath(), "/api/admin/")
	collection, rawID, hasID := strings.Cut(rest, "/")
	id := ""
	if hasID {
		var err error
		if id, err = url.PathUnescape(rawID); err != nil || !safePath(id, true) {
			http.NotFound(w, r)
			return
		}
	}
	switch {
	case collection == "canonical-names" && !hasID:
		if allow(w, r, http.MethodGet) {
			s.serveCanonicalNames(w, r)
		}
	case collection == "known-platforms" && !hasID:
		if allow(w, r, http.MethodGet) {
			writeJSON(w, http.StatusOK, store.KnownPlatforms())
		}
	case collection == "platforms" && !hasID:
		if allow(w, r, http.MethodGet, http.MethodPost) {
			s.servePlatforms(w, r)
		}
	case collection == "platforms":
		if allow(w, r, http.MethodDelete) {
			s.adminResult(w, s.store.DeletePlatform(r.Context(), id), http.StatusNoContent)
		}
	case collection == "games" && !hasID:
		if allow(w, r, http.MethodGet, http.MethodPost) {
			s.serveGames(w, r)
		}
	case collection == "games":
		if allow(w, r, http.MethodGet, http.MethodPut, http.MethodDelete) {
			s.serveGame(w, r, id)
		}
	default:
		http.NotFound(w, r)
	}
}

func allow(w http.ResponseWriter, r *http.Request, methods ...string) bool {
	for _, m := range methods {
		if r.Method == m {
			return true
		}
	}
	w.Header().Set("Allow", strings.Join(methods, ", "))
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAdminBody))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		http.Error(w, "invalid request body: "+err.Error(), http.StatusBadRequest)
		return false
	}
	if _, err := d.Token(); err != io.EOF {
		http.Error(w, "invalid request body: trailing data", http.StatusBadRequest)
		return false
	}
	return true
}

func etag(revision int64) string { return fmt.Sprintf("\"%d\"", revision) }

// ifMatch parses the required If-Match revision; a missing or malformed header is a failed precondition.
func ifMatch(w http.ResponseWriter, r *http.Request) (int64, bool) {
	value := r.Header.Get("If-Match")
	revision, err := strconv.ParseInt(strings.Trim(value, `"`), 10, 64)
	if value == "" || err != nil || value != etag(revision) {
		http.Error(w, "If-Match with the game revision is required", http.StatusPreconditionFailed)
		return 0, false
	}
	return revision, true
}

// adminResult maps store errors to HTTP statuses and writes status on success.
func (s *Server) adminResult(w http.ResponseWriter, err error, status int) bool {
	var missing *store.MissingError
	switch {
	case err == nil:
		s.load() // refresh the public snapshot now; a failure resurfaces on the next request
		if status != 0 {
			w.WriteHeader(status)
		}
		return true
	case errors.As(err, &missing):
		writeJSON(w, http.StatusUnprocessableEntity, struct {
			Missing []content.MissingSource `json:"missing"`
		}{missing.Missing})
	case errors.Is(err, store.ErrNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, store.ErrExists), errors.Is(err, store.ErrInUse):
		http.Error(w, err.Error(), http.StatusConflict)
	case errors.Is(err, store.ErrConflict):
		http.Error(w, err.Error(), http.StatusPreconditionFailed)
	case errors.Is(err, store.ErrUnknownPlatform):
		http.Error(w, err.Error(), http.StatusBadRequest)
	default:
		log.Printf("admin: %v", err)
		http.Error(w, "storage error: "+err.Error(), http.StatusInternalServerError)
	}
	return false
}

func (s *Server) servePlatforms(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		platforms, err := s.store.Platforms(r.Context())
		if err != nil {
			s.adminResult(w, err, 0)
			return
		}
		writeJSON(w, http.StatusOK, platforms)
		return
	}
	var p content.Platform
	if !decodeBody(w, r, &p) {
		return
	}
	if err := content.ValidPlatform(p); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.adminResult(w, s.store.CreatePlatform(r.Context(), p), http.StatusCreated)
}

func (s *Server) serveGames(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var g content.Game
		if !decodeBody(w, r, &g) {
			return
		}
		if err := g.Validate(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		revision, err := s.store.CreateGame(r.Context(), g, r.URL.Query().Get("allow_missing") == "1")
		if err == nil {
			w.Header().Set("ETag", etag(revision))
			w.Header().Set("Location", "/api/admin/games/"+url.PathEscape(g.ID))
		}
		s.adminResult(w, err, http.StatusCreated)
		return
	}
	params := r.URL.Query()
	f := store.Filter{Query: params.Get("q"), PlatformID: params.Get("platform_id"), Count: 50}
	for name, dst := range map[string]*int{"skip": &f.Skip, "count": &f.Count} {
		if v := params.Get(name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				http.Error(w, "invalid "+name, http.StatusBadRequest)
				return
			}
			*dst = n
		}
	}
	games, err := s.store.Games(r.Context(), f)
	if err != nil {
		s.adminResult(w, err, 0)
		return
	}
	writeJSON(w, http.StatusOK, games)
}

func (s *Server) serveGame(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		g, revision, available, missing, err := s.store.Game(r.Context(), id)
		if err != nil {
			s.adminResult(w, err, 0)
			return
		}
		w.Header().Set("ETag", etag(revision))
		writeJSON(w, http.StatusOK, struct {
			Game      content.Game            `json:"game"`
			Available bool                    `json:"available"`
			Missing   []content.MissingSource `json:"missing"`
		}{g, available, missing})
	case http.MethodPut:
		revision, ok := ifMatch(w, r)
		if !ok {
			return
		}
		var g content.Game
		if !decodeBody(w, r, &g) {
			return
		}
		if g.ID != id {
			http.Error(w, "game id must match the URL", http.StatusBadRequest)
			return
		}
		if err := g.Validate(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		revision, err := s.store.UpdateGame(r.Context(), g, revision, r.URL.Query().Get("allow_missing") == "1")
		if err == nil {
			w.Header().Set("ETag", etag(revision))
		}
		s.adminResult(w, err, http.StatusNoContent)
	case http.MethodDelete:
		revision, ok := ifMatch(w, r)
		if ok {
			s.adminResult(w, s.store.DeleteGame(r.Context(), id, revision), http.StatusNoContent)
		}
	}
}

// AdminFS serves admin static assets from an in-memory filesystem when non-nil.
var AdminFS fs.FS

func serveAdminFiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == "/admin" {
		http.Redirect(w, r, "/admin/", http.StatusMovedPermanently)
		return
	}
	rel := strings.TrimPrefix(r.URL.Path, "/admin/")
	if rel == "" {
		rel = "index.html"
	}
	if path.Clean(rel) != rel || rel == ".." || strings.HasPrefix(rel, "../") || path.IsAbs(rel) || strings.Contains(rel, "\\") {
		http.NotFound(w, r)
		return
	}
	if AdminFS != nil {
		f, err := AdminFS.Open(rel)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil || info.IsDir() {
			http.NotFound(w, r)
			return
		}
		rs, ok := f.(io.ReadSeeker)
		if !ok {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, info.Name(), info.ModTime(), rs)
		return
	}
	root := filepath.Join("web", "dist")
	for _, dir := range []string{"web", root} {
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			http.Error(w, "admin build not found; build web/dist first", http.StatusServiceUnavailable)
			return
		}
	}
	if info, err := os.Stat(filepath.Join(root, "index.html")); err != nil || !info.Mode().IsRegular() {
		http.Error(w, "admin build not found; build web/dist first", http.StatusServiceUnavailable)
		return
	}
	filename := root
	for _, part := range strings.Split(rel, "/") {
		filename = filepath.Join(filename, part)
		info, err := os.Lstat(filename)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			http.NotFound(w, r)
			return
		}
	}
	f, err := os.Open(filename)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}
