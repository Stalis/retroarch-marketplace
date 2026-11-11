package content

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const gamesJSON = `[
    {"id":"single","canonical_name":"Single","display_name":"Single edition","platform_id":"psx",
     "discs":[{"entrypoint":"Single/game.cue","files":[
       {"source":"one.cue","path":"Single/game.cue"},
       {"source":"one.bin","path":"Single/game.bin"}]}]},
    {"id":"multi","canonical_name":"Multi","platform_id":"psx","playlist":"Playlists/game.m3u",
     "discs":[
       {"entrypoint":"Disc 2/two.cue","files":[{"source":"two.cue","path":"Disc 2/two.cue"}]},
       {"entrypoint":"Disc 1/one.cue","files":[{"source":"one.cue","path":"Disc 1/one.cue"}]}
     ]}
]`

const originalCue = "FILE \"game.bin\" BINARY\r\n  TRACK 01 MODE2/2352\r\n    INDEX 01 00:00:00\r\n"

var psx = Platform{ID: "psx", Name: "PlayStation", LibretroName: "Sony - PlayStation"}

func parseGames(t *testing.T, data string) []Game {
	t.Helper()
	var games []Game
	d := json.NewDecoder(strings.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(&games); err != nil {
		t.Fatal(err)
	}
	return games
}

func sourceDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name, data := range map[string]string{"one.cue": originalCue, "one.bin": "\x00binary\xff", "two.cue": "second cue\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestArchivePathConflicts(t *testing.T) {
	for _, tc := range []struct {
		name, first, second, playlist string
		valid                         bool
	}{
		{"parent first", "a", "a/b", "list.m3u", false},
		{"child first", "a/b", "a", "list.m3u", false},
		{"duplicate", "a", "a", "list.m3u", false},
		{"playlist parent", "a/b", "c", "a", false},
		{"playlist child", "a", "c", "a/b", false},
		{"component prefix", "a", "ab/b", "list.m3u", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			games := parseGames(t, fmt.Sprintf(`[{"id":"g","canonical_name":"G","platform_id":"p","playlist":%q,"discs":[{"entrypoint":%q,"files":[{"source":"one.cue","path":%q}]},{"entrypoint":%q,"files":[{"source":"two.cue","path":%q}]}]}]`, tc.playlist, tc.first, tc.first, tc.second, tc.second))
			if err := games[0].Validate(); (err == nil) != tc.valid {
				t.Fatalf("valid=%v: %v", tc.valid, err)
			}
		})
	}
}

func archiveFiles(t *testing.T, name string) map[string]string {
	t.Helper()
	z, err := zip.OpenReader(name)
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	files := make(map[string]string)
	for _, f := range z.File {
		if _, exists := files[f.Name]; exists {
			t.Fatalf("duplicate ZIP entry %q", f.Name)
		}
		if !f.Mode().IsRegular() {
			t.Fatalf("non-regular ZIP entry %q", f.Name)
		}
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		if closeErr := r.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			t.Fatal(err)
		}
		files[f.Name] = string(data)
	}
	return files
}

func TestSingleAndMultiDisc(t *testing.T) {
	dir := sourceDir(t)
	wantFiles := []map[string]string{
		{"Single/game.cue": originalCue, "Single/game.bin": "\x00binary\xff"},
		{"Disc 2/two.cue": "second cue\n", "Disc 1/one.cue": originalCue, "Playlists/game.m3u": "../Disc 2/two.cue\n../Disc 1/one.cue\n"},
	}
	for i, g := range parseGames(t, gamesJSON) {
		want := []struct{ id, name, display, entry string }{
			{"single", "Single", "Single edition", "Single/game.cue"},
			{"multi", "Multi", "", "Playlists/game.m3u"},
		}[i]
		item, err := NewItem(g, psx, dir)
		if err != nil {
			t.Fatal(err)
		}
		var c struct {
			ID            string   `json:"id"`
			CanonicalName string   `json:"canonical_name"`
			DisplayName   string   `json:"display_name"`
			Platform      Platform `json:"platform"`
			Entrypoint    string   `json:"entrypoint"`
			Files         []string `json:"files"`
			DownloadURL   string   `json:"download_url"`
		}
		if err := json.Unmarshal(item.Card, &c); err != nil {
			t.Fatal(err)
		}
		if item.ID != want.id || c.ID != want.id || c.CanonicalName != want.name || c.DisplayName != want.display || c.Entrypoint != want.entry || c.DownloadURL != "packages/"+want.id+".zip" || c.Platform != psx {
			t.Errorf("unexpected card: %+v", c)
		}
		archive := filepath.Join(t.TempDir(), want.id+".zip")
		if err := item.WriteZIP(archive); err != nil {
			t.Fatal(err)
		}
		actual := archiveFiles(t, archive)
		if !reflect.DeepEqual(actual, wantFiles[i]) {
			t.Errorf("archive %s: %#v", want.id, actual)
		}
		listed := make(map[string]bool)
		for _, f := range c.Files {
			listed[f] = true
		}
		if len(listed) != len(c.Files) || len(listed) != len(actual) {
			t.Errorf("catalog files %v, archive %v", c.Files, actual)
		}
		for f := range actual {
			if !listed[f] {
				t.Errorf("%q absent from catalog", f)
			}
		}
	}
	if cue, err := os.ReadFile(filepath.Join(dir, "one.cue")); err != nil || string(cue) != originalCue {
		t.Fatalf("source CUE changed: %q, %v", cue, err)
	}
}

func TestRejectsInvalidGames(t *testing.T) {
	cases := []struct{ name, old, replacement string }{
		{"unsafe source", `"source":"one.bin"`, `"source":"sub/../one.bin"`},
		{"absolute source", `"source":"one.bin"`, `"source":"/one.bin"`},
		{"unsafe archive path", `"path":"Single/game.bin"`, `"path":"../game.bin"`},
		{"duplicate archive path", `"path":"Single/game.bin"`, `"path":"Single/game.cue"`},
		{"missing entrypoint", `"entrypoint":"Single/game.cue"`, `"entrypoint":"Single/missing.cue"`},
		{"unsafe id", `"id":"single"`, `"id":"../single"`},
		{"empty name", `"canonical_name":"Single"`, `"canonical_name":""`},
		{"playlist collision", `"playlist":"Playlists/game.m3u"`, `"playlist":"Disc 1/one.cue"`},
		{"unsafe playlist", `"playlist":"Playlists/game.m3u"`, `"playlist":"../game.m3u"`},
		{"multidisc without playlist", `,"playlist":"Playlists/game.m3u"`, ``},
		{"playlist comment reference", `Disc 2/two.cue`, `Playlists/#two.cue`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := strings.Replace(gamesJSON, tc.old, tc.replacement, -1)
			if data == gamesJSON {
				t.Fatalf("fixture replacement did not match %q", tc.old)
			}
			valid := 0
			for _, g := range parseGames(t, data) {
				if g.Validate() == nil {
					valid++
				}
			}
			if valid != 1 {
				t.Fatalf("expected exactly one game rejected, %d valid", valid)
			}
		})
	}
}

func TestValidPlatform(t *testing.T) {
	for _, p := range []Platform{{"", "N", "L"}, {"a/b", "N", "L"}, {"a", "", "L"}, {"a", "N", ".."}} {
		if ValidPlatform(p) == nil {
			t.Errorf("accepted %+v", p)
		}
	}
	if err := ValidPlatform(psx); err != nil {
		t.Fatal(err)
	}
}
