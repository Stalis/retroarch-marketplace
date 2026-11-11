package content

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type Platform struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	LibretroName string `json:"libretro_name"`
}

type File struct {
	Source string `json:"source"`
	Path   string `json:"path"`
}

type Disc struct {
	Entrypoint string `json:"entrypoint"`
	Files      []File `json:"files"`
}

// Game is the stored definition of one catalog entry; sources are relative to the database directory.
type Game struct {
	ID            string  `json:"id"`
	CanonicalName string  `json:"canonical_name"`
	DisplayName   *string `json:"display_name,omitempty"`
	PlatformID    string  `json:"platform_id"`
	Discs         []Disc  `json:"discs"`
	Playlist      *string `json:"playlist,omitempty"`
}

type card struct {
	ID            string   `json:"id"`
	CanonicalName string   `json:"canonical_name"`
	DisplayName   *string  `json:"display_name,omitempty"`
	Platform      Platform `json:"platform"`
	Entrypoint    string   `json:"entrypoint"`
	Files         []string `json:"files"`
	DownloadURL   string   `json:"download_url"`
}

type MissingSource struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

type Item struct {
	ID          string
	Card        json.RawMessage
	Fingerprint string
	card        card
	files       []File
	playlist    string
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

// SafePath reports whether s is a clean relative slash path (a single component when component is true).
func SafePath(s string, component bool) bool { return safePath(s, component) }

func validText(s string) bool { return s != "" && !strings.ContainsRune(s, 0) }

func ValidPlatform(p Platform) error {
	if !safePath(p.ID, true) || !validText(p.Name) || !safePath(p.LibretroName, true) {
		return fmt.Errorf("invalid platform %q", p.ID)
	}
	return nil
}

// Validate checks names, archive paths and the playlist without touching the filesystem.
func (g Game) Validate() error {
	if !safePath(g.ID, true) {
		return fmt.Errorf("invalid or duplicate content id %q", g.ID)
	}
	if !validText(g.CanonicalName) || (g.DisplayName != nil && !validText(*g.DisplayName)) {
		return fmt.Errorf("invalid content name for %q", g.ID)
	}
	if len(g.Discs) == 0 || (len(g.Discs) > 1 && g.Playlist == nil) {
		return fmt.Errorf("content %q requires discs and a playlist for multiple discs", g.ID)
	}
	paths := map[string]bool{}
	addPath := func(name string) bool {
		if _, exists := paths[name]; exists {
			return false
		}
		for i := 0; i < len(name); i++ {
			if name[i] == '/' && paths[name[:i]] {
				return false
			}
		}
		for i := 0; i < len(name); i++ {
			if name[i] == '/' {
				paths[name[:i]] = false
			}
		}
		paths[name] = true
		return true
	}
	for _, disc := range g.Discs {
		if !safePath(disc.Entrypoint, false) || len(disc.Files) == 0 {
			return fmt.Errorf("invalid disc in %q", g.ID)
		}
		found := false
		for _, f := range disc.Files {
			if !safePath(f.Path, false) || !safePath(f.Source, false) || !addPath(f.Path) {
				return fmt.Errorf("invalid or duplicate file in %q: %q", g.ID, f.Path)
			}
			if f.Path == disc.Entrypoint {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("disc entrypoint %q is not in its files", disc.Entrypoint)
		}
	}
	if g.Playlist != nil {
		if !safePath(*g.Playlist, false) || !addPath(*g.Playlist) {
			return fmt.Errorf("invalid or duplicate playlist %q", *g.Playlist)
		}
		if _, err := playlist(g); err != nil {
			return err
		}
	}
	return nil
}

func playlist(g Game) (string, error) {
	var lines []string
	for _, disc := range g.Discs {
		rel, err := filepath.Rel(filepath.FromSlash(path.Dir(*g.Playlist)), filepath.FromSlash(disc.Entrypoint))
		if err != nil {
			return "", err
		}
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "#") || strings.ContainsAny(rel, "\r\n") || path.Clean(path.Join(path.Dir(*g.Playlist), rel)) != disc.Entrypoint {
			return "", fmt.Errorf("unsafe playlist reference %q", rel)
		}
		lines = append(lines, rel)
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// NewItem builds the public card and package plan of a validated game; root anchors relative sources.
func NewItem(g Game, p Platform, root string) (Item, error) {
	if err := g.Validate(); err != nil {
		return Item{}, err
	}
	item := Item{ID: g.ID, card: card{ID: g.ID, CanonicalName: g.CanonicalName, DisplayName: g.DisplayName, Platform: p, Entrypoint: g.Discs[0].Entrypoint, DownloadURL: "packages/" + url.PathEscape(g.ID) + ".zip"}}
	for _, disc := range g.Discs {
		for _, f := range disc.Files {
			item.files = append(item.files, File{Source: filepath.Join(root, filepath.FromSlash(f.Source)), Path: f.Path})
			item.card.Files = append(item.card.Files, f.Path)
		}
	}
	if g.Playlist != nil {
		var err error
		if item.playlist, err = playlist(g); err != nil {
			return Item{}, err
		}
		item.card.Entrypoint = *g.Playlist
		item.card.Files = append([]string{*g.Playlist}, item.card.Files...)
	}
	var err error
	item.Card, err = json.Marshal(item.card)
	return item, err
}

// Playlist returns the generated M3U text, empty for single-disc games.
func (item Item) Playlist() string { return item.playlist }

// Sources returns the absolute source paths in package order.
func (item Item) Sources() []string {
	out := make([]string, len(item.files))
	for i, f := range item.files {
		out[i] = f.Source
	}
	return out
}

func (item Item) WriteZIP(filename string) (err error) {
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	defer func() {
		if e := file.Close(); err == nil {
			err = e
		}
	}()
	zw := zip.NewWriter(file)
	defer func() {
		if e := zw.Close(); err == nil {
			err = e
		}
	}()
	for _, f := range item.files {
		input, e := os.Open(f.Source)
		if e != nil {
			return e
		}
		info, e := input.Stat()
		if e != nil {
			input.Close()
			return e
		}
		if !info.Mode().IsRegular() {
			input.Close()
			return fmt.Errorf("source %q is not a regular file", f.Source)
		}
		out, e := zw.Create(f.Path)
		if e == nil {
			_, e = io.Copy(out, input)
		}
		closeErr := input.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if item.playlist != "" {
		out, e := zw.Create(item.card.Entrypoint)
		if e != nil {
			return e
		}
		if _, e = io.WriteString(out, item.playlist); e != nil {
			return e
		}
	}
	return nil
}
