package catalog

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdminMissingConfirmation(t *testing.T) {
	second := strings.Replace(card, `"example"`, `"second"`, 1)
	manifest, packages := fixture(t, "["+card+","+second+"]", map[string][]zipEntry{
		"example": {{name: "game.txt", data: "first"}, {name: "extra.txt", data: "extra"}},
		"second": {{name: "game.txt", data: "second"}},
	})
	h, err := New(manifest, packages)
	if err != nil {
		t.Fatal(err)
	}
	get := adminRequest(h, "GET", "/api/admin/manifest", nil, "")
	original, tag := append([]byte(nil), get.Body.Bytes()...), get.Header().Get("ETag")
	candidate := bytes.Replace(original, []byte("source-0-0"), []byte("absent/first"), 1)
	candidate = bytes.Replace(candidate, []byte("source-0-1"), []byte("absent/extra"), 1)
	w := adminRequest(h, "PUT", "/api/admin/manifest", candidate, tag)
	var response struct {
		Missing []struct {
			ID   string `json:"id"`
			Path string `json:"path"`
		} `json:"missing"`
	}
	if w.Code != http.StatusUnprocessableEntity || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") || json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response.Missing) != 2 || response.Missing[0].ID != "example" || response.Missing[0].Path != "absent/first" || response.Missing[1].ID != "example" || response.Missing[1].Path != "absent/extra" {
		t.Fatalf("missing response: %d %s", w.Code, w.Body.String())
	}
	if got, err := os.ReadFile(manifest); err != nil || !bytes.Equal(got, original) {
		t.Fatalf("prompt changed manifest: %v", err)
	}
	w = adminRequest(h, "PUT", "/api/admin/manifest?allow_missing=1", candidate, `"stale"`)
	if w.Code != http.StatusPreconditionFailed {
		t.Fatalf("stale confirmation: %d %s", w.Code, w.Body.String())
	}
	// A candidate cannot approve its own new missing sources without confirmation.
	preapproved := bytes.Replace(candidate, []byte(`"id":"example"`), []byte(`"defer_missing":true,"id":"example"`), 1)
	w = adminRequest(h, "PUT", "/api/admin/manifest", preapproved, tag)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unconfirmed flag: %d %s", w.Code, w.Body.String())
	}
	w = adminRequest(h, "PUT", "/api/admin/manifest?allow_missing=1", candidate, tag)
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") || w.Header().Get("ETag") != manifestETag(w.Body.Bytes()) {
		t.Fatalf("confirmation: %d %s", w.Code, w.Body.String())
	}
	saved, err := os.ReadFile(manifest)
	if err != nil || !bytes.Equal(saved, w.Body.Bytes()) {
		t.Fatalf("saved response differs: %v", err)
	}
	var doc struct {
		Contents []struct {
			ID string `json:"id"`
			Defer bool `json:"defer_missing"`
		} `json:"contents"`
	}
	if err := json.Unmarshal(saved, &doc); err != nil || len(doc.Contents) != 2 || doc.Contents[0].ID != "example" || !doc.Contents[0].Defer || doc.Contents[1].Defer {
		t.Fatalf("saved flags: %s (%v)", saved, err)
	}
	// Editing an unrelated field does not prompt for already approved sources.
	updated := bytes.Replace(saved, []byte("Étoile Game"), []byte("Renamed Game"), 1)
	w = adminRequest(h, "PUT", "/api/admin/manifest", updated, manifestETag(saved))
	if w.Code != http.StatusNoContent || w.Header().Get("ETag") != manifestETag(updated) {
		t.Fatalf("deferred edit: %d %s", w.Code, w.Body.String())
	}
	// Adding a different missing source to an already deferred game requires confirmation.
	newMissing := bytes.Replace(updated, []byte("absent/first"), []byte("absent/other"), 1)
	w = adminRequest(h, "PUT", "/api/admin/manifest", newMissing, manifestETag(updated))
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "absent/other") || strings.Contains(w.Body.String(), "absent/extra") {
		t.Fatalf("new missing source: %d %s", w.Code, w.Body.String())
	}
	// Deferred games remain unavailable after restart, while other games serve normally.
	h, err = New(manifest, packages)
	if err != nil {
		t.Fatal(err)
	}
	games := request(t, h, "GET", "/api/games")
	if games.Code != 200 || strings.Contains(games.Body.String(), `"id":"example"`) || !strings.Contains(games.Body.String(), `"id":"second"`) {
		t.Fatalf("deferred catalog: %d %s", games.Code, games.Body.String())
	}
	asset := "/assets/cores/Sony%20-%20PlayStation/example.zip"
	for _, target := range []string{"/api/packages/example.zip", asset} {
		if w := request(t, h, "GET", target); w.Code != 404 {
			t.Fatalf("missing %s: %d", target, w.Code)
		}
	}
	if w := request(t, h, "GET", "/api/packages/second.zip"); w.Code != 200 {
		t.Fatalf("unaffected package: %d %s", w.Code, w.Body.String())
	}
	if err := os.Mkdir(filepath.Join(filepath.Dir(manifest), "absent"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first", "extra"} {
		if err := os.WriteFile(filepath.Join(filepath.Dir(manifest), "absent", name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	games = request(t, h, "GET", "/api/games")
	if games.Code != 200 || !strings.Contains(games.Body.String(), `"id":"example"`) || !strings.Contains(games.Body.String(), `"id":"second"`) {
		t.Fatalf("restored catalog: %d %s", games.Code, games.Body.String())
	}
	for _, target := range []string{"/api/packages/example.zip", asset} {
		if w := request(t, h, "GET", target); w.Code != 200 {
			t.Fatalf("restored %s: %d %s", target, w.Code, w.Body.String())
		}
	}
}

func TestAdminMissingCannotOverrideInvalidSources(t *testing.T) {
	manifest, packages := fixture(t, "["+card+"]", map[string][]zipEntry{
		"example": {{name: "game.txt", data: "first"}, {name: "extra.txt", data: "extra"}},
	})
	h, err := New(manifest, packages)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(filepath.Dir(manifest), "directory"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"../unsafe", "directory"} {
		candidate := bytes.Replace(original, []byte("source-0-0"), []byte("missing"), 1)
		candidate = bytes.Replace(candidate, []byte("source-0-1"), []byte(bad), 1)
		w := adminRequest(h, "PUT", "/api/admin/manifest?allow_missing=1", candidate, manifestETag(original))
		if w.Code != http.StatusBadRequest {
			t.Errorf("invalid %q: %d %s", bad, w.Code, w.Body.String())
		}
		if got, err := os.ReadFile(manifest); err != nil || !bytes.Equal(got, original) {
			t.Fatalf("invalid %q changed manifest: %v", bad, err)
		}
	}
	duplicate := bytes.Replace(original, []byte("source-0-0"), []byte("missing"), 1)
	duplicate = bytes.Replace(duplicate, []byte(`"path":"extra.txt"`), []byte(`"path":"game.txt"`), 1)
	w := adminRequest(h, "PUT", "/api/admin/manifest?allow_missing=1", duplicate, manifestETag(original))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("duplicate path: %d %s", w.Code, w.Body.String())
	}
	if got, err := os.ReadFile(manifest); err != nil || !bytes.Equal(got, original) {
		t.Fatalf("duplicate path changed manifest: %v", err)
	}
}

func TestAdminMissingPreservesFieldSpellingAndValues(t *testing.T) {
	manifest, packages := fixture(t, "["+card+"]", map[string][]zipEntry{
		"example": {{name: "game.txt", data: "first"}},
	})
	h, err := New(manifest, packages)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	candidate := bytes.Replace(original, []byte(`"contents"`), []byte(`"Contents"`), 1)
	candidate = bytes.Replace(candidate, []byte(`"id":"example"`), []byte(`"ID":"example","defer_missing":false,"Defer_Missing":false`), 1)
	candidate = bytes.Replace(candidate, []byte("source-0-0"), []byte("missing"), 1)
	w := adminRequest(h, "PUT", "/api/admin/manifest?allow_missing=1", candidate, manifestETag(original))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"Contents"`) || !strings.Contains(w.Body.String(), `"ID":"example"`) || !strings.Contains(w.Body.String(), `"defer_missing":true`) || !strings.Contains(w.Body.String(), `"Defer_Missing":true`) {
		t.Fatalf("preserving JSON keys: %d %s", w.Code, w.Body.String())
	}
	if _, err := New(manifest, packages); err != nil {
		t.Fatalf("saved manifest invalid: %v", err)
	}
}

func TestAdminConcurrentMissingConfirmations(t *testing.T) {
	manifest, packages := fixture(t, "["+card+"]", map[string][]zipEntry{
		"example": {{name: "game.txt", data: "first"}},
	})
	h, err := New(manifest, packages)
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	candidate := bytes.Replace(original, []byte("source-0-0"), []byte("missing"), 1)
	results := make(chan int, 2)
	for range 2 {
		go func() {
			results <- adminRequest(h, "PUT", "/api/admin/manifest?allow_missing=1", candidate, manifestETag(original)).Code
		}()
	}
	first, second := <-results, <-results
	if !((first == http.StatusOK && second == http.StatusPreconditionFailed) || (first == http.StatusPreconditionFailed && second == http.StatusOK)) {
		t.Fatalf("concurrent confirmations: %d, %d", first, second)
	}
}
