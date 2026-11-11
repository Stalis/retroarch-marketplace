package catalog

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"

	"retroarch-marketplace/internal/content"
	"retroarch-marketplace/internal/store"
)

func (s *Server) serveCustomAPI(w http.ResponseWriter, r *http.Request, snapshot *catalogSnapshot, path string, item content.Item, isPackage bool) {
	if isPackage {
		packagePath, err := s.archive(item)
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
	ids, err := s.store.GameIDs(r.Context(), store.Filter{Query: query, LibretroName: platform, AvailableOnly: true, Skip: skip, Count: count})
	if err != nil {
		http.Error(w, "catalog unavailable", http.StatusInternalServerError)
		return
	}
	results := make([]map[string]json.RawMessage, 0, len(ids))
	for _, id := range ids {
		// Games the snapshot lacks became available after it was built; the next request includes them.
		if g, ok := snapshot.byID[id]; ok {
			results = append(results, g.fields)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(results)
}
