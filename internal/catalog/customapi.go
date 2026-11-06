package catalog

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"unicode"
	"unicode/utf8"

	"retroarch-marketplace/internal/content"
)

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

func (s *Server) serveCustomAPI(w http.ResponseWriter, r *http.Request, snapshot *catalogSnapshot, path string, item content.Item, isPackage bool) {
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
	if path == "/api/platforms" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(snapshot.platforms)
		return
	}
	query := params.Get("q")
	platform := params.Get("platform")
	skip, count := 0, -1
	for name, dst := range map[string]*int{"skip": &skip, "count": &count} {
		if v := params.Get(name); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				http.Error(w, "invalid "+name, http.StatusBadRequest)
				return
			}
			*dst = n
		}
	}
	results := make([]map[string]json.RawMessage, 0)
	for _, g := range snapshot.games {
		if platform != "" && g.platform != platform {
			continue
		}
		if !containsFold(g.canonical, query) && !containsFold(g.display, query) {
			continue
		}
		if skip > 0 {
			skip--
			continue
		}
		if count >= 0 && len(results) >= count {
			break
		}
		results = append(results, g.fields)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(results)
}
