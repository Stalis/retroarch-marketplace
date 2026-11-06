package content

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
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

type sourceFile struct {
	Source string `json:"source"`
	Path   string `json:"path"`
}

type disc struct {
	Entrypoint string       `json:"entrypoint"`
	Files      []sourceFile `json:"files"`
}

type content struct {
	ID            string  `json:"id"`
	CanonicalName string  `json:"canonical_name"`
	DisplayName   *string `json:"display_name,omitempty"`
	PlatformID    string  `json:"platform_id"`
	Discs         []disc  `json:"discs"`
	Playlist      *string `json:"playlist,omitempty"`
	DeferMissing  bool    `json:"defer_missing,omitempty"`
}

type manifest struct {
	Platforms []Platform `json:"platforms"`
	Contents  []content  `json:"contents"`
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
	ID           string
	Card         json.RawMessage
	Available    bool
	DeferMissing bool
	Missing      []MissingSource
	card         card
	files        []sourceFile
	playlist     string
}

type Snapshot struct {
	Items     []Item
	Platforms []Platform
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

func validText(s string) bool { return s != "" && !strings.ContainsRune(s, 0) }

// scanJSON rejects duplicate object members before decoding the typed manifest.
func scanJSON(d *json.Decoder) error {
	t, err := d.Token()
	if err != nil {
		return err
	}
	if t == nil {
		return errors.New("null is not allowed in the source manifest")
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return fmt.Errorf("duplicate or invalid JSON field %v", key)
			}
			seen[name] = true
			if err := scanJSON(d); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := scanJSON(d); err != nil {
				return err
			}
		}
	default:
		return errors.New("invalid JSON value")
	}
	_, err = d.Token()
	return err
}

func Load(source string) (*Snapshot, string, error) {
	snapshot, fingerprint, err := Inspect(source)
	if err != nil {
		return nil, "", err
	}
	for _, item := range snapshot.Items {
		if !item.DeferMissing && len(item.Missing) != 0 {
			return nil, "", fmt.Errorf("source %q: %w", item.Missing[0].Path, os.ErrNotExist)
		}
	}
	return snapshot, fingerprint, nil
}

// Inspect validates the manifest and reports missing sources without approving them.
func Inspect(source string) (*Snapshot, string, error) {
	source, err := filepath.Abs(source)
	if err != nil {
		return nil, "", err
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return nil, "", err
	}
	hash := sha256.New()
	fmt.Fprintf(hash, "%q:%q;", source, data)
	d := json.NewDecoder(bytes.NewReader(data))
	if err := scanJSON(d); err != nil {
		return nil, "", fmt.Errorf("source JSON: %w", err)
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, "", errors.New("source JSON has trailing data")
	}
	var m manifest
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&m); err != nil {
		return nil, "", fmt.Errorf("source JSON: %w", err)
	}
	if m.Platforms == nil || m.Contents == nil {
		return nil, "", errors.New("platforms and contents must be arrays")
	}
	platforms := map[string]Platform{}
	uniquePlatforms := make([]Platform, 0, len(m.Platforms))
	seen := map[string]bool{}
	for _, p := range m.Platforms {
		if !safePath(p.ID, true) || !validText(p.Name) || !safePath(p.LibretroName, true) {
			return nil, "", fmt.Errorf("invalid platform %q", p.ID)
		}
		if _, exists := platforms[p.ID]; exists {
			return nil, "", fmt.Errorf("duplicate platform %q", p.ID)
		}
		platforms[p.ID] = p
		if !seen[p.LibretroName] {
			seen[p.LibretroName] = true
			uniquePlatforms = append(uniquePlatforms, p)
		}
	}
	ids := map[string]bool{}
	result := make([]Item, 0, len(m.Contents))
	for _, c := range m.Contents {
		if !safePath(c.ID, true) || ids[c.ID] {
			return nil, "", fmt.Errorf("invalid or duplicate content id %q", c.ID)
		}
		ids[c.ID] = true
		p, ok := platforms[c.PlatformID]
		if !ok {
			return nil, "", fmt.Errorf("unknown platform %q", c.PlatformID)
		}
		if !validText(c.CanonicalName) || (c.DisplayName != nil && !validText(*c.DisplayName)) {
			return nil, "", fmt.Errorf("invalid content name for %q", c.ID)
		}
		if len(c.Discs) == 0 || (len(c.Discs) > 1 && c.Playlist == nil) {
			return nil, "", fmt.Errorf("content %q requires discs and a playlist for multiple discs", c.ID)
		}
		item := Item{ID: c.ID, Available: true, DeferMissing: c.DeferMissing, card: card{ID: c.ID, CanonicalName: c.CanonicalName, DisplayName: c.DisplayName, Platform: p, DownloadURL: "packages/" + url.PathEscape(c.ID) + ".zip"}}
		paths := map[string]bool{}
		for _, disc := range c.Discs {
			if !safePath(disc.Entrypoint, false) || len(disc.Files) == 0 {
				return nil, "", fmt.Errorf("invalid disc in %q", c.ID)
			}
			found := false
			for _, f := range disc.Files {
				if !safePath(f.Path, false) || paths[f.Path] || !safePath(f.Source, false) {
					return nil, "", fmt.Errorf("invalid or duplicate file in %q: %q", c.ID, f.Path)
				}
				paths[f.Path] = true
				if f.Path == disc.Entrypoint {
					found = true
				}
				sourcePath := f.Source
				f.Source = filepath.Join(filepath.Dir(source), filepath.FromSlash(sourcePath))
				info, err := os.Stat(f.Source)
				if err != nil {
					if !os.IsNotExist(err) {
						return nil, "", fmt.Errorf("source %q: %w", f.Source, err)
					}
					item.Available = false
					item.Missing = append(item.Missing, MissingSource{ID: c.ID, Path: sourcePath})
					fmt.Fprintf(hash, "%q:missing;", f.Source)
				} else {
					if !info.Mode().IsRegular() {
						return nil, "", fmt.Errorf("source %q is not a regular file", f.Source)
					}
					fmt.Fprintf(hash, "%q:available:%d:%d;", f.Source, info.Size(), info.ModTime().UnixNano())
				}
				item.files = append(item.files, f)
				item.card.Files = append(item.card.Files, f.Path)
			}
			if !found {
				return nil, "", fmt.Errorf("disc entrypoint %q is not in its files", disc.Entrypoint)
			}
		}
		item.card.Entrypoint = c.Discs[0].Entrypoint
		if c.Playlist != nil {
			if !safePath(*c.Playlist, false) || paths[*c.Playlist] {
				return nil, "", fmt.Errorf("invalid or duplicate playlist %q", *c.Playlist)
			}
			var lines []string
			for _, disc := range c.Discs {
				rel, err := filepath.Rel(filepath.FromSlash(path.Dir(*c.Playlist)), filepath.FromSlash(disc.Entrypoint))
				if err != nil {
					return nil, "", err
				}
				rel = filepath.ToSlash(rel)
				if strings.HasPrefix(rel, "#") || strings.ContainsAny(rel, "\r\n") || path.Clean(path.Join(path.Dir(*c.Playlist), rel)) != disc.Entrypoint {
					return nil, "", fmt.Errorf("unsafe playlist reference %q", rel)
				}
				lines = append(lines, rel)
			}
			item.playlist = strings.Join(lines, "\n") + "\n"
			item.card.Entrypoint = *c.Playlist
			item.card.Files = append([]string{*c.Playlist}, item.card.Files...)
		}
		item.Card, err = json.Marshal(item.card)
		if err != nil {
			return nil, "", err
		}
		result = append(result, item)
	}
	return &Snapshot{Items: result, Platforms: uniquePlatforms}, fmt.Sprintf("%x", hash.Sum(nil)), nil
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
