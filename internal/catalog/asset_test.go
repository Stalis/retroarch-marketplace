package catalog

import (
	"archive/zip"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path"
	"reflect"
	"strings"
	"testing"
)

func assetLabel(s string) string {
	return "~" + hex.EncodeToString([]byte(s))
}

func assetIndex(t *testing.T, h http.Handler, target string) []string {
	t.Helper()
	w := request(t, h, http.MethodGet, target)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s: %d %s", target, w.Code, w.Body.String())
	}
	body := w.Body.String()
	if body == "" {
		return []string{}
	}
	if !strings.HasSuffix(body, "\n") {
		t.Fatalf("index %s missing final newline: %q", target, body)
	}
	return strings.Split(strings.TrimSuffix(body, "\n"), "\n")
}

func TestAssetDownloader(t *testing.T) {
	const platform = "Sony - PlayStation"
	const unusualPlatform = "平台; 100% #"
	ids := []string{"first game", "second game", "游戏; 100% #"}
	platforms := []string{platform, platform, unusualPlatform}
	contents := [][]zipEntry{
		{{name: "game.m3u", data: "Disc/game.cue\n"}, {name: "Disc/game.cue", data: "FILE \"game.bin\" BINARY\n"}, {name: "Disc/game.bin", data: "first disc"}},
		{{name: "game.m3u", data: "Disc/game.cue\n"}, {name: "Disc/game.cue", data: "FILE \"game.bin\" BINARY\n"}, {name: "Disc/game.bin", data: "second disc"}},
		{{name: "game.m3u", data: "Disc/game.cue\n"}, {name: "Disc/game.cue", data: "FILE \"game.bin\" BINARY\n"}, {name: "Disc/game.bin", data: "third disc"}},
	}
	var cards []map[string]interface{}
	packages := make(map[string][]zipEntry)
	for i, id := range ids {
		cards = append(cards, map[string]interface{}{
			"id": id, "canonical_name": "Game " + id,
			"platform":   map[string]string{"id": "psx", "name": "PlayStation", "libretro_name": platforms[i]},
			"entrypoint": "game.m3u", "files": []string{"game.m3u", "Disc/game.cue", "Disc/game.bin"}, "download_url": "original.zip",
		})
		packages[id] = contents[i]
	}
	manifest, err := json.Marshal(cards)
	if err != nil {
		t.Fatal(err)
	}
	catalog, dir := fixture(t, string(manifest), packages)
	h, err := New(catalog, dir)
	if err != nil {
		t.Fatal(err)
	}
	categories := assetIndex(t, h, "/assets/cores/.index-dirs")
	wantCategories := []string{platform, assetLabel(unusualPlatform)}
	if !reflect.DeepEqual(categories, wantCategories) {
		t.Fatalf("categories %q, want %q", categories, wantCategories)
	}
	labels := []string{"first game", "second game", assetLabel(ids[2])}
	for i, category := range categories {
		base := "/assets/cores/" + url.PathEscape(category)
		redirect := request(t, h, http.MethodGet, base)
		if redirect.Code != http.StatusMovedPermanently || redirect.Header().Get("Location") != base+"/" {
			t.Fatalf("redirect %s: %d Location=%q", base, redirect.Code, redirect.Header().Get("Location"))
		}
		want := []string{labels[0] + ".zip", labels[1] + ".zip"}
		if i == 1 {
			want = []string{labels[2] + ".zip"}
		}
		index := assetIndex(t, h, base+"/.index")
		if !reflect.DeepEqual(index, want) {
			t.Fatalf("index %s: %q, want %q", base, index, want)
		}
		if got := request(t, h, http.MethodGet, base+"/.index-dirs").Code; got != http.StatusNotFound {
			t.Errorf("subdirectories %s: %d", base, got)
		}
	}
	if entries := cacheEntries(t, dir); len(entries) != 0 {
		t.Fatalf("indexes created archives: %v", entries)
	}

	allNames := make(map[string]bool)
	for i, id := range ids {
		category := platform
		if i == 2 {
			category = assetLabel(unusualPlatform)
		}
		target := "/assets/cores/" + url.PathEscape(category) + "/" + url.PathEscape(labels[i]+".zip")
		w := request(t, h, http.MethodGet, target)
		if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/zip") {
			t.Fatalf("download %s: %d %v", target, w.Code, w.Header())
		}
		if i == 0 {
			if entries := cacheEntries(t, dir); len(entries) != 1 {
				t.Fatalf("asset download cache: %v", entries)
			}
			api := request(t, h, http.MethodGet, "/api/packages/"+url.PathEscape(id)+".zip")
			if api.Code != http.StatusOK || len(cacheEntries(t, dir)) != 1 {
				t.Fatalf("API did not reuse asset cache: %d", api.Code)
			}
		}
		zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
		if err != nil {
			t.Fatal(err)
		}
		files := make(map[string]string)
		for _, f := range zr.File {
			if allNames[f.Name] {
				t.Fatalf("archives collide at %q", f.Name)
			}
			allNames[f.Name] = true
			r, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(r)
			if err != nil {
				t.Fatal(err)
			}
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
			files[f.Name] = string(data)
		}
		if len(files) != len(contents[i]) {
			t.Fatalf("archive %s: unexpected members %v", id, files)
		}
		prefix := labels[i] + "/"
		for _, original := range contents[i] {
			if got, ok := files[prefix+original.name]; !ok || got != original.data {
				t.Fatalf("archive %s missing or altered %q: %q", id, prefix+original.name, got)
			}
		}
		m3u := prefix + "game.m3u"
		cue := path.Join(path.Dir(m3u), strings.TrimSpace(files[m3u]))
		cueText, ok := files[cue]
		if !ok || !strings.HasPrefix(cueText, "FILE \"") {
			t.Fatalf("M3U target missing: %q", cue)
		}
		binName := strings.SplitN(strings.TrimPrefix(cueText, "FILE \""), "\"", 2)[0]
		if _, ok := files[path.Join(path.Dir(cue), binName)]; !ok {
			t.Fatalf("CUE target missing: %q", binName)
		}
		post := request(t, h, http.MethodPost, target)
		if post.Code != http.StatusMethodNotAllowed || post.Header().Get("Allow") != "GET" {
			t.Errorf("POST %s: %d Allow=%q", target, post.Code, post.Header().Get("Allow"))
		}
	}
	for _, target := range []string{"/assets/cores/.index-dirs", "/assets/cores/" + url.PathEscape(platform), "/assets/cores/" + url.PathEscape(platform) + "/.index"} {
		w := request(t, h, http.MethodPost, target)
		if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "GET" {
			t.Errorf("POST %s: %d Allow=%q", target, w.Code, w.Header().Get("Allow"))
		}
	}
	for _, target := range []string{"/assets/", "/assets/cores/", "/assets/cores/missing/.index", "/assets/cores/" + url.PathEscape(platform) + "/missing.zip", "/assets/cores/" + url.PathEscape(platform) + "/game.m3u", "/assets/cores/" + url.PathEscape(platform) + "/%2e%2e/catalog.json"} {
		if got := request(t, h, http.MethodGet, target).Code; got != http.StatusNotFound {
			t.Errorf("GET %s: %d, want 404", target, got)
		}
	}
	api := request(t, h, http.MethodGet, "/api/games")
	var gotCards []map[string]interface{}
	if api.Code != http.StatusOK || json.Unmarshal(api.Body.Bytes(), &gotCards) != nil || len(gotCards) != len(cards) {
		t.Fatalf("existing API: %d %s", api.Code, api.Body.String())
	}
	for i, id := range ids {
		if gotCards[i]["id"] != id || gotCards[i]["download_url"] != "packages/"+url.PathEscape(id)+".zip" {
			t.Errorf("existing API card: %v", gotCards[i])
		}
	}
}

func TestAssetEmptyCatalog(t *testing.T) {
	catalog, packages := fixture(t, `[]`, nil)
	h, err := New(catalog, packages)
	if err != nil {
		t.Fatal(err)
	}
	if got := assetIndex(t, h, "/assets/cores/.index-dirs"); len(got) != 0 {
		t.Fatalf("empty catalog categories: %q", got)
	}
	if got := request(t, h, http.MethodGet, "/assets/cores/missing/.index").Code; got != http.StatusNotFound {
		t.Fatalf("missing category: %d", got)
	}
}

func TestAssetCaseCollisions(t *testing.T) {
	ids := []string{"Game", "game", "third"}
	platforms := []string{"System", "System", "system"}
	var cards []map[string]interface{}
	packages := make(map[string][]zipEntry)
	for i, id := range ids {
		cards = append(cards, map[string]interface{}{
			"id": id, "canonical_name": id,
			"platform":   map[string]string{"id": "p", "name": "Platform", "libretro_name": platforms[i]},
			"entrypoint": "file.txt", "files": []string{"file.txt"}, "download_url": "original.zip",
		})
		packages[id] = []zipEntry{{name: "file.txt", data: id}}
	}
	manifest, err := json.Marshal(cards)
	if err != nil {
		t.Fatal(err)
	}
	catalog, dir := fixture(t, string(manifest), packages)
	h, err := New(catalog, dir)
	if err != nil {
		t.Fatal(err)
	}
	wantCategories := []string{assetLabel("System"), assetLabel("system")}
	if got := assetIndex(t, h, "/assets/cores/.index-dirs"); !reflect.DeepEqual(got, wantCategories) {
		t.Fatalf("case-colliding categories %q, want %q", got, wantCategories)
	}
	base := "/assets/cores/" + url.PathEscape(wantCategories[0]) + "/"
	wantGames := []string{assetLabel("Game") + ".zip", assetLabel("game") + ".zip"}
	if got := assetIndex(t, h, base+".index"); !reflect.DeepEqual(got, wantGames) {
		t.Fatalf("case-colliding games %q, want %q", got, wantGames)
	}
	for _, name := range wantGames {
		if got := request(t, h, http.MethodGet, base+url.PathEscape(name)).Code; got != http.StatusOK {
			t.Errorf("download %q: %d", name, got)
		}
	}
}
