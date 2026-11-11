package catalog

import (
	"encoding/json"
	"errors"
	"net/http"

	"retroarch-marketplace/internal/canonicalnames"
)

var boxartClient = canonicalnames.NewClient()

func (s *Server) serveCanonicalNames(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	result, err := s.finder.Titles(r.Context(), r.URL.Query().Get("platform"), r.URL.Query().Get("q"))
	if err != nil {
		if errors.Is(err, canonicalnames.ErrInvalidInput) {
			http.Error(w, "invalid platform or query", http.StatusBadRequest)
		} else {
			http.Error(w, "upstream error", http.StatusBadGateway)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
