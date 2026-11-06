package content

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const sourceManifest = `{
  "platforms":[{"id":"psx","name":"PlayStation","libretro_name":"Sony - PlayStation"}],
  "contents":[
    {"id":"single","canonical_name":"Single","display_name":"Single edition","platform_id":"psx",
     "discs":[{"entrypoint":"Single/game.cue","files":[
       {"source":"one.cue","path":"Single/game.cue"},
       {"source":"one.bin","path":"Single/game.bin"}]}]},
    {"id":"multi","canonical_name":"Multi","platform_id":"psx","playlist":"Playlists/game.m3u",
     "discs":[
       {"entrypoint":"Disc 2/two.cue","files":[{"source":"two.cue","path":"Disc 2/two.cue"}]},
       {"entrypoint":"Disc 1/one.cue","files":[{"source":"one.cue","path":"Disc 1/one.cue"}]}
     ]}
  ]
}`

const originalCue = "FILE \"game.bin\" BINARY\r\n  TRACK 01 MODE2/2352\r\n    INDEX 01 00:00:00\r\n"

func sourceFixture(t *testing.T, manifest string) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	for name, data := range map[string]string{"source.json": manifest, "one.cue": originalCue, "one.bin": "\x00binary\xff", "two.cue": "second cue\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return dir, filepath.Join(dir, "source.json"), filepath.Join(dir, "output")
}

func TestSnapshotRetainsValidatedPlatforms(t *testing.T) {
	manifest := strings.Replace(sourceManifest,
		`"platforms":[{"id":"psx","name":"PlayStation","libretro_name":"Sony - PlayStation"}]`,
		`"platforms":[{"id":"psx","name":"PlayStation","libretro_name":"Sony - PlayStation"},{"id":"duplicate","name":"Duplicate","libretro_name":"Sony - PlayStation"},{"id":"empty","name":"Empty","libretro_name":"Empty"}]`, 1)
	_, source, _ := sourceFixture(t, manifest)
	for _, load := range []struct {
		name string
		fn   func(string) (*Snapshot, string, error)
	}{{"Load", Load}, {"Inspect", Inspect}} {
		t.Run(load.name, func(t *testing.T) {
			if err := os.WriteFile(source, []byte(manifest), 0600); err != nil {
				t.Fatal(err)
			}
			snapshot, _, err := load.fn(source)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(source, []byte(`{"platforms":[],"contents":[]}`), 0600); err != nil {
				t.Fatal(err)
			}
			want := []Platform{{ID: "psx", Name: "PlayStation", LibretroName: "Sony - PlayStation"}, {ID: "empty", Name: "Empty", LibretroName: "Empty"}}
			if !reflect.DeepEqual(snapshot.Platforms, want) || len(snapshot.Items) != 2 || snapshot.Items[0].card.Platform != want[0] {
				t.Fatalf("snapshot does not retain validated manifest: %+v", snapshot)
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
	dir, source, output := sourceFixture(t, sourceManifest)
	snapshot, _, err := Load(source)
	if err != nil {
		t.Fatal(err)
	}
	items := snapshot.Items
	var cards []struct {
		ID            string `json:"id"`
		CanonicalName string `json:"canonical_name"`
		DisplayName   string `json:"display_name"`
		Platform      struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			LibretroName string `json:"libretro_name"`
		} `json:"platform"`
		Entrypoint  string   `json:"entrypoint"`
		Files       []string `json:"files"`
		DownloadURL string   `json:"download_url"`
	}
	for _, item := range items {
		var c struct {
			ID            string `json:"id"`
			CanonicalName string `json:"canonical_name"`
			DisplayName   string `json:"display_name"`
			Platform      struct {
				ID           string `json:"id"`
				Name         string `json:"name"`
				LibretroName string `json:"libretro_name"`
			} `json:"platform"`
			Entrypoint  string   `json:"entrypoint"`
			Files       []string `json:"files"`
			DownloadURL string   `json:"download_url"`
		}
		if err := json.Unmarshal(item.Card, &c); err != nil {
			t.Fatal(err)
		}
		cards = append(cards, c)
	}
	if len(cards) != 2 {
		t.Fatalf("items: %v", items)
	}
	wantFiles := []map[string]string{
		{"Single/game.cue": originalCue, "Single/game.bin": "\x00binary\xff"},
		{"Disc 2/two.cue": "second cue\n", "Disc 1/one.cue": originalCue, "Playlists/game.m3u": "../Disc 2/two.cue\n../Disc 1/one.cue\n"},
	}
	for i, want := range []struct{ id, name, display, entry string }{
		{"single", "Single", "Single edition", "Single/game.cue"},
		{"multi", "Multi", "", "Playlists/game.m3u"},
	} {
		c := cards[i]
		if c.ID != want.id || c.CanonicalName != want.name || c.DisplayName != want.display || c.Entrypoint != want.entry || c.DownloadURL != "packages/"+want.id+".zip" || c.Platform.ID != "psx" || c.Platform.Name != "PlayStation" || c.Platform.LibretroName != "Sony - PlayStation" {
			t.Errorf("unexpected card: %+v", c)
		}
		if items[i].ID != want.id {
			t.Fatalf("item ID: %s", items[i].ID)
		}
		archive := output + want.id + ".zip"
		if err := items[i].WriteZIP(archive); err != nil {
			t.Fatal(err)
		}
		actual := archiveFiles(t, archive)
		if !reflect.DeepEqual(actual, wantFiles[i]) {
			t.Errorf("archive %s: %#v", want.id, actual)
		}
		listed := make(map[string]bool)
		for _, f := range c.Files {
			if listed[f] {
				t.Errorf("duplicate catalog file %q", f)
			}
			listed[f] = true
		}
		if len(listed) != len(actual) {
			t.Errorf("catalog files %v, archive %v", c.Files, actual)
		}
		for f := range actual {
			if !listed[f] {
				t.Errorf("%q absent from catalog", f)
			}
		}
	}
	original, err := os.ReadFile(filepath.Join(dir, "one.cue"))
	if err != nil || !bytes.Equal(original, []byte(originalCue)) {
		t.Fatalf("source CUE changed: %q, %v", original, err)
	}
	manifest, err := os.ReadFile(source)
	if err != nil || string(manifest) != sourceManifest {
		t.Fatalf("source manifest changed: %v", err)
	}
}

func TestRejectsInvalidSources(t *testing.T) {
	cases := []struct{ name, old, replacement string }{
		{"missing source", `"source":"one.bin"`, `"source":"missing.bin"`},
		{"invalid defer flag", `"id":"single"`, `"id":"single","defer_missing":null`},
		{"unsafe source", `"source":"one.bin"`, `"source":"sub/../one.bin"`},
		{"unsafe archive path", `"path":"Single/game.bin"`, `"path":"../game.bin"`},
		{"duplicate archive path", `"path":"Single/game.bin"`, `"path":"Single/game.cue"`},
		{"missing entrypoint", `"entrypoint":"Single/game.cue"`, `"entrypoint":"Single/missing.cue"`},
		{"unknown platform", `"platform_id":"psx"`, `"platform_id":"missing"`},
		{"unsafe id", `"id":"single"`, `"id":"../single"`},
		{"duplicate platform id", `{"id":"psx","name":"PlayStation","libretro_name":"Sony - PlayStation"}`, `{"id":"psx","name":"PlayStation","libretro_name":"Sony - PlayStation"},{"id":"psx","name":"Other","libretro_name":"Other"}`},
		{"duplicate content id", `"id":"multi"`, `"id":"single"`},
		{"playlist collision", `"playlist":"Playlists/game.m3u"`, `"playlist":"Disc 1/one.cue"`},
		{"unsafe playlist", `"playlist":"Playlists/game.m3u"`, `"playlist":"../game.m3u"`},
		{"multidisc without playlist", `,"playlist":"Playlists/game.m3u"`, ``},
		{"playlist comment reference", `Disc 2/two.cue`, `Playlists/#two.cue`},
		{"trailing JSON", "  ]\n}", "  ]\n} {}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manifest := strings.Replace(sourceManifest, tc.old, tc.replacement, -1)
			if manifest == sourceManifest {
				t.Fatalf("fixture replacement did not match %q", tc.old)
			}
			dir, source, _ := sourceFixture(t, manifest)
			if err := os.Mkdir(filepath.Join(dir, "sub"), 0700); err != nil {
				t.Fatal(err)
			}
			if _, _, err := Load(source); err == nil {
				t.Fatal("accepted invalid source")
			}
			cue, err := os.ReadFile(filepath.Join(dir, "one.cue"))
			if err != nil || string(cue) != originalCue {
				t.Fatalf("source changed: %v", err)
			}
			data, err := os.ReadFile(source)
			if err != nil || string(data) != manifest {
				t.Fatalf("manifest changed: %v", err)
			}
		})
	}
}

func TestAcceptsSymlinkedSource(t *testing.T) {
	dir, source, output := sourceFixture(t, sourceManifest)
	if err := os.Rename(filepath.Join(dir, "one.cue"), filepath.Join(dir, "original.cue")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("original.cue", filepath.Join(dir, "one.cue")); err != nil {
		t.Fatal(err)
	}
	snapshot, _, err := Load(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := snapshot.Items[0].WriteZIP(output); err != nil {
		t.Fatal(err)
	}
	files := archiveFiles(t, output)
	if files["Single/game.cue"] != originalCue {
		t.Fatalf("symlinked CUE changed: %q", files["Single/game.cue"])
	}
}

func TestFingerprintTracksManifestPathAndSourceMetadata(t *testing.T) {
	dir, source, _ := sourceFixture(t, sourceManifest)
	fingerprint := func(name string) string {
		t.Helper()
		_, sum, err := Load(name)
		if err != nil {
			t.Fatal(err)
		}
		return sum
	}
	first := fingerprint(source)
	if first != fingerprint(source) {
		t.Fatal("fingerprint changed without a change")
	}
	other := filepath.Join(dir, "other.json")
	if err := os.WriteFile(other, []byte(sourceManifest), 0600); err != nil {
		t.Fatal(err)
	}
	if first == fingerprint(other) {
		t.Fatal("manifest path did not change fingerprint")
	}
	if err := os.WriteFile(source, []byte(sourceManifest+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	second := fingerprint(source)
	if first == second {
		t.Fatal("manifest bytes did not change fingerprint")
	}
	file := filepath.Join(dir, "one.bin")
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	mtime := info.ModTime().Add(time.Second)
	if err := os.Chtimes(file, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	third := fingerprint(source)
	if second == third {
		t.Fatal("source mtime did not change fingerprint")
	}
	if err := os.WriteFile(file, []byte("different size"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	if third == fingerprint(source) {
		t.Fatal("source size did not change fingerprint")
	}
}

func TestInspectDeferredMissing(t *testing.T) {
	manifest := strings.Replace(sourceManifest, `"id":"single"`, `"id":"single","defer_missing":true`, 1)
	manifest = strings.Replace(manifest, `"source":"one.cue"`, `"source":"absent/one.cue"`, 1)
	manifest = strings.Replace(manifest, `"source":"one.bin"`, `"source":"absent/one.bin"`, 1)
	dir, source, _ := sourceFixture(t, manifest)
	snapshot, missingFingerprint, err := Load(source)
	if err != nil {
		t.Fatal(err)
	}
	items := snapshot.Items
	want := []MissingSource{{ID: "single", Path: "absent/one.cue"}, {ID: "single", Path: "absent/one.bin"}}
	if items[0].Available || !items[0].DeferMissing || !reflect.DeepEqual(items[0].Missing, want) || !items[1].Available {
		t.Fatalf("unexpected availability: %+v", items)
	}
	if err := os.Mkdir(filepath.Join(dir, "absent"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"one.cue", "one.bin"} {
		if err := os.WriteFile(filepath.Join(dir, "absent", name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, availableFingerprint, err := Load(source)
	if err != nil {
		t.Fatal(err)
	}
	items = snapshot.Items
	if !items[0].Available || len(items[0].Missing) != 0 || missingFingerprint == availableFingerprint {
		t.Fatalf("activation: items=%+v fingerprint changed=%v err=%v", items, missingFingerprint != availableFingerprint, err)
	}
}

func TestInspectUnapprovedAndInvalidMissing(t *testing.T) {
	manifest := strings.Replace(sourceManifest, `"source":"one.bin"`, `"source":"missing/file"`, 1)
	_, source, _ := sourceFixture(t, manifest)
	snapshot, _, err := Inspect(source)
	if err != nil || snapshot.Items[0].Available || !reflect.DeepEqual(snapshot.Items[0].Missing, []MissingSource{{ID: "single", Path: "missing/file"}}) {
		t.Fatalf("inspection: %+v, %v", snapshot, err)
	}
	if _, _, err := Load(source); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unapproved missing: %v", err)
	}
	manifest = strings.Replace(manifest, `"entrypoint":"Single/game.cue"`, `"entrypoint":"Single/missing.cue"`, 1)
	if err := os.WriteFile(source, []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Inspect(source); err == nil {
		t.Fatal("missing source masked invalid entrypoint")
	}
}

func TestDeferredDoesNotAllowOtherFilesystemErrors(t *testing.T) {
	manifest := strings.Replace(sourceManifest, `"id":"single"`, `"id":"single","defer_missing":true`, 1)
	manifest = strings.Replace(manifest, `"source":"one.bin"`, `"source":"restricted/file"`, 1)
	dir, source, _ := sourceFixture(t, manifest)
	restricted := filepath.Join(dir, "restricted")
	if err := os.Mkdir(restricted, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(restricted, "file"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Inspect(source); err == nil {
		t.Fatal("accepted directory as source")
	}
	if err := os.Chmod(restricted, 0000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(restricted, 0700)
	if _, err := os.Stat(filepath.Join(restricted, "file")); os.IsPermission(err) {
		if _, _, err := Inspect(source); !errors.Is(err, os.ErrPermission) {
			t.Fatalf("permission error overridden: %v", err)
		}
	}
}
