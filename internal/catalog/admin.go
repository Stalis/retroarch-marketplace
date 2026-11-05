package catalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"retroarch-marketplace/internal/content"
)

const maxManifestSize = 4 << 20

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

func manifestETag(data []byte) string {
	return fmt.Sprintf("\"%x\"", sha256.Sum256(data))
}

// markDeferred edits only the effective contents array, preserving other JSON values and key order.
func markDeferred(candidate []byte, affected map[string]bool) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(candidate))
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	start, end := -1, -1
	var contents []json.RawMessage
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, err
		}
		if strings.EqualFold(key.(string), "contents") {
			end = int(decoder.InputOffset())
			start = end - len(raw)
			if err := json.Unmarshal(raw, &contents); err != nil {
				return nil, err
			}
		}
	}
	if start < 0 {
		return nil, fmt.Errorf("missing contents")
	}
	for i, raw := range contents {
		var game struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &game); err != nil {
			return nil, err
		}
		if !affected[game.ID] {
			continue
		}
		d := json.NewDecoder(bytes.NewReader(raw))
		if _, err := d.Token(); err != nil {
			return nil, err
		}
		var edited bytes.Buffer
		last := 0
		found := false
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return nil, err
			}
			var value json.RawMessage
			if err := d.Decode(&value); err != nil {
				return nil, err
			}
			if strings.EqualFold(key.(string), "defer_missing") {
				valueEnd := int(d.InputOffset())
				valueStart := valueEnd - len(value)
				edited.Write(raw[last:valueStart])
				edited.WriteString("true")
				last = valueEnd
				found = true
			}
		}
		if found {
			edited.Write(raw[last:])
		} else {
			edited.Write(raw[:len(raw)-1])
			edited.WriteString(`,"defer_missing":true}`)
		}
		contents[i] = edited.Bytes()
	}
	encoded, err := json.Marshal(contents)
	if err != nil {
		return nil, err
	}
	result := make([]byte, 0, len(candidate)-(end-start)+len(encoded))
	result = append(result, candidate[:start]...)
	result = append(result, encoded...)
	result = append(result, candidate[end:]...)
	return result, nil
}

func (s *Server) serveAdmin(w http.ResponseWriter, r *http.Request) {
	if Version != "" {
		w.Header().Set("X-Catalog-Version", Version)
	}
	if !localAdmin(r) {
		http.Error(w, "admin access requires a loopback client and host", http.StatusForbidden)
		return
	}
	if r.URL.Path != "/api/admin/manifest" {
		serveAdminFiles(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPut {
		w.Header().Set("Allow", "GET, PUT")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.Method == http.MethodPut {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		if r.Header.Get("Origin") != scheme+"://"+r.Host {
			http.Error(w, "origin must match the request host", http.StatusForbidden)
			return
		}
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			http.Error(w, "Content-Type must be application/json", http.StatusBadRequest)
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	original, err := os.ReadFile(s.catalogPath)
	if err != nil {
		http.Error(w, "cannot read manifest: "+err.Error(), http.StatusInternalServerError)
		return
	}
	etag := manifestETag(original)
	if r.Method == http.MethodGet {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", etag)
		_, _ = w.Write(original)
		return
	}
	if r.Header.Get("If-Match") != etag {
		http.Error(w, "manifest revision does not match", http.StatusPreconditionFailed)
		return
	}
	info, err := os.Lstat(s.catalogPath)
	if err != nil || !info.Mode().IsRegular() {
		http.Error(w, "manifest must be a regular file, not a symlink", http.StatusForbidden)
		return
	}
	candidate, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxManifestSize))
	if err != nil {
		http.Error(w, "manifest exceeds size limit or cannot be read", http.StatusBadRequest)
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.catalogPath), ".manifest-*")
	if err != nil {
		http.Error(w, "cannot create temporary manifest: "+err.Error(), http.StatusInternalServerError)
		return
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err = tmp.Chmod(info.Mode().Perm()); err == nil {
		_, err = tmp.Write(candidate)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		http.Error(w, "cannot write temporary manifest: "+err.Error(), http.StatusInternalServerError)
		return
	}
	items, _, err := content.Inspect(name)
	if err != nil {
		http.Error(w, "invalid manifest: "+err.Error(), http.StatusBadRequest)
		return
	}
	approved := make(map[content.MissingSource]bool)
	if previous, _, inspectErr := content.Inspect(s.catalogPath); inspectErr == nil {
		for _, item := range previous {
			if item.DeferMissing {
				for _, missing := range item.Missing {
					approved[missing] = true
				}
			}
		}
	}
	var missing []content.MissingSource
	affected := make(map[string]bool)
	for _, item := range items {
		for _, source := range item.Missing {
			if !item.DeferMissing || !approved[source] {
				missing = append(missing, source)
				affected[item.ID] = true
			}
		}
	}
	changed := len(missing) > 0 && r.URL.Query().Get("allow_missing") == "1"
	if len(missing) > 0 && !changed {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = json.NewEncoder(w).Encode(struct {
			Missing []content.MissingSource `json:"missing"`
		}{missing})
		return
	}
	if changed {
		candidate, err = markDeferred(candidate, affected)
		if err == nil {
			var updated *os.File
			updated, err = os.OpenFile(name, os.O_WRONLY|os.O_TRUNC, 0)
			if err == nil {
				_, err = updated.Write(candidate)
				if err == nil {
					err = updated.Sync()
				}
				if closeErr := updated.Close(); err == nil {
					err = closeErr
				}
			}
		}
		if err != nil {
			http.Error(w, "cannot prepare manifest: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if _, _, err = content.Load(name); err != nil {
		http.Error(w, "invalid manifest: "+err.Error(), http.StatusBadRequest)
		return
	}
	currentInfo, err := os.Lstat(s.catalogPath)
	if err != nil || !currentInfo.Mode().IsRegular() || !os.SameFile(info, currentInfo) {
		http.Error(w, "manifest changed during update", http.StatusPreconditionFailed)
		return
	}
	current, err := os.ReadFile(s.catalogPath)
	if err != nil || !bytes.Equal(original, current) {
		http.Error(w, "manifest changed during update", http.StatusPreconditionFailed)
		return
	}
	if err = os.Rename(name, s.catalogPath); err != nil {
		http.Error(w, "cannot replace manifest: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("ETag", manifestETag(candidate))
	if changed {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(candidate)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
