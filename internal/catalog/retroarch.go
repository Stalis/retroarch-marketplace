package catalog

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type assetRoute struct {
	index, redirect, archive, prefix string
	isIndex                          bool
	isAssetsBundle                   bool
}

// UpstreamAssetsURL is the official Libretro buildbot URL for frontend assets.
var UpstreamAssetsURL = "https://buildbot.libretro.com/assets/frontend/assets.zip"

func isSafeAssetChar(c rune) bool {
	return (c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') ||
		c == ' ' || c == '-' || c == '_' ||
		c == '(' || c == ')' ||
		c == '[' || c == ']' ||
		c == '\'' || c == '.' ||
		c == '!' || c == ',' ||
		c == '&' || c == '+' || c == ':'
}

// assetLabels avoids RetroArch's line delimiters and case-insensitive filesystem collisions.
func assetLabels(values []string) map[string]string {
	labels := make(map[string]string, len(values))
	counts := make(map[string]int, len(values))
	for _, value := range values {
		upper := strings.ToUpper(value)
		reserved := upper == "CON" || upper == "PRN" || upper == "AUX" || upper == "NUL" ||
			(len(upper) == 4 && (strings.HasPrefix(upper, "COM") || strings.HasPrefix(upper, "LPT")) && upper[3] >= '1' && upper[3] <= '9')
		if _, exists := labels[value]; exists {
			continue
		}
		label := value
		if strings.TrimSpace(value) != value || value == "" || reserved {
			label = "~" + hex.EncodeToString([]byte(value))
		} else {
			for _, c := range value {
				if !isSafeAssetChar(c) {
					label = "~" + hex.EncodeToString([]byte(value))
					break
				}
			}
		}
		labels[value] = label
		counts[strings.ToLower(label)]++
	}
	for value, label := range labels {
		if counts[strings.ToLower(label)] > 1 {
			labels[value] = "~" + hex.EncodeToString([]byte(value))
		}
	}
	return labels
}

func assetRoutes(games []game) map[string]assetRoute {
	const root = "/assets/cores/"
	routes := make(map[string]assetRoute)
	groups := make(map[string][]game)
	for _, g := range games {
		groups[g.platform] = append(groups[g.platform], g)
	}
	platforms := make([]string, 0, len(groups))
	for platform := range groups {
		platforms = append(platforms, platform)
	}
	labels := assetLabels(platforms)
	categories := make([]string, 0, len(platforms))
	for _, platform := range platforms {
		category := labels[platform]
		categories = append(categories, category)
		base := root + url.PathEscape(category)
		routes[base] = assetRoute{redirect: base + "/"}
		gameList := groups[platform]
		sort.Slice(gameList, func(i, j int) bool { return gameList[i].id < gameList[j].id })
		names := make([]string, 0, len(gameList))
		counts := make(map[string]int)
		for _, g := range gameList {
			name := g.canonical
			if name == "" {
				name = g.id
			}
			names = append(names, name)
			counts[strings.ToLower(name)]++
		}
		gameLabels := assetLabels(names)
		used := make(map[string]bool)
		allocated := make([]string, len(gameList))
		for i, name := range names {
			if counts[strings.ToLower(name)] == 1 {
				allocated[i] = gameLabels[name]
				used[strings.ToLower(allocated[i])] = true
			}
		}
		for i, g := range gameList {
			if allocated[i] != "" {
				continue
			}
			baseName := "~" + hex.EncodeToString([]byte(names[i])) + "-" + hex.EncodeToString([]byte(g.id))
			name := baseName
			for n := 1; used[strings.ToLower(name)]; n++ {
				name = fmt.Sprintf("%s-%d", baseName, n)
			}
			allocated[i] = name
			used[strings.ToLower(name)] = true
		}
		entries := make([]string, 0, len(gameList))
		aliases := make(map[string]int)
		for _, g := range gameList {
			aliases[strings.ToLower(g.id+".zip")]++
		}
		for i, g := range gameList {
			name := allocated[i]
			entry := name + ".zip"
			entries = append(entries, entry)
			routes[base+"/"+url.PathEscape(entry)] = assetRoute{archive: g.id, prefix: name + "/"}
		}
		for i, g := range gameList {
			idEntry := g.id + ".zip"
			if aliases[strings.ToLower(idEntry)] == 1 && !used[strings.ToLower(g.id)] {
				routes[base+"/"+url.PathEscape(idEntry)] = assetRoute{archive: g.id, prefix: allocated[i] + "/"}
			}
		}
		sort.Strings(entries)
		routes[base+"/.index"] = assetRoute{index: strings.Join(entries, "\n") + "\n", isIndex: true}
		routes[base+"/"] = routes[base+"/.index"]
	}
	sort.Strings(categories)
	index := strings.Join(categories, "\n")
	if index != "" {
		index += "\n"
	}
	routes[root+".index-dirs"] = assetRoute{index: index, isIndex: true}
	routes["/assets/frontend/assets.zip"] = assetRoute{isAssetsBundle: true}
	routes["/assets/assets.zip"] = assetRoute{isAssetsBundle: true}
	return routes
}

func (s *Server) serveRetroArch(w http.ResponseWriter, r *http.Request, snapshot *catalogSnapshot, asset assetRoute) {
	if asset.isAssetsBundle {
		s.serveAssetsZip(w, r)
		return
	}
	if !asset.isIndex && asset.redirect == "" {
		item := snapshot.packages["/api/packages/"+url.PathEscape(asset.archive)+".zip"]
		var err error
		asset.archive, err = s.archive(item)
		if err != nil {
			http.Error(w, "package unavailable", http.StatusInternalServerError)
			return
		}
		defer s.releaseArchive(asset.archive)
	}
	asset.serve(w, r)
}

func (a assetRoute) serve(w http.ResponseWriter, r *http.Request) {
	if a.redirect != "" {
		http.Redirect(w, r, a.redirect, http.StatusMovedPermanently)
		return
	}
	if a.isIndex {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = io.WriteString(w, a.index)
		return
	}
	archive, err := zip.OpenReader(a.archive)
	if err != nil {
		http.Error(w, "package unavailable", http.StatusInternalServerError)
		return
	}
	defer archive.Close()
	w.Header().Set("Content-Type", "application/zip")
	writer := zip.NewWriter(w)
	defer writer.Close()
	for _, member := range archive.File {
		header := member.FileHeader
		header.Name = a.prefix + member.Name
		dst, err := writer.CreateRaw(&header)
		if err != nil {
			panic(http.ErrAbortHandler)
		}
		src, err := member.OpenRaw()
		if err != nil {
			panic(http.ErrAbortHandler)
		}
		if _, err := io.Copy(dst, src); err != nil {
			panic(http.ErrAbortHandler)
		}
	}
	if err := writer.Close(); err != nil {
		panic(http.ErrAbortHandler)
	}
}

type assetsFlight struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
}

func (s *Server) serveLocalAssets(w http.ResponseWriter, r *http.Request) bool {
	for _, candidate := range []string{
		filepath.Join(s.packagesDir, ".cache", "assets.zip"),
		filepath.Join(s.packagesDir, "assets.zip"),
		"assets.zip",
	} {
		if f, err := os.Open(candidate); err == nil {
			info, statErr := f.Stat()
			if statErr == nil && info.Mode().IsRegular() {
				w.Header().Set("Content-Type", "application/zip")
				http.ServeContent(w, r, "assets.zip", info.ModTime(), f)
				f.Close()
				return true
			}
			f.Close()
		}
	}
	return false
}

func (s *Server) fetchAssets(ctx context.Context, limit int64) error {
	for _, candidate := range []string{filepath.Join(s.packagesDir, ".cache", "assets.zip"), filepath.Join(s.packagesDir, "assets.zip"), "assets.zip"} {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return nil
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, UpstreamAssetsURL, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("assets upstream: %s", resp.Status)
	}
	if err := os.MkdirAll(s.packagesDir, 0755); err != nil {
		return err
	}
	info, err := os.Lstat(s.packagesDir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("invalid assets cache root")
	}
	dir := filepath.Join(s.packagesDir, ".cache")
	if err := os.Mkdir(dir, 0755); err != nil && !os.IsExist(err) {
		return err
	}
	info, err = os.Lstat(dir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("invalid assets cache directory")
	}
	f, err := os.CreateTemp(dir, ".assets-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	n, err := io.Copy(f, io.LimitReader(resp.Body, limit+1))
	if err == nil && n > limit {
		err = fmt.Errorf("assets bundle exceeds size limit")
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	s.assetsMu.Lock()
	defer s.assetsMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, "assets.zip"))
}

func (s *Server) waitAssets(ctx context.Context) {
	s.assetsMu.Lock()
	flight := s.assetsFetch
	if flight == nil {
		fetchCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		flight = &assetsFlight{done: make(chan struct{}), cancel: cancel}
		s.assetsFetch = flight
		go func() {
			defer cancel()
			_ = s.fetchAssets(fetchCtx, 512<<20)
			s.assetsMu.Lock()
			if s.assetsFetch == flight {
				s.assetsFetch = nil
			}
			close(flight.done)
			s.assetsMu.Unlock()
		}()
	}
	flight.waiters++
	s.assetsMu.Unlock()
	select {
	case <-flight.done:
	case <-ctx.Done():
	}
	s.assetsMu.Lock()
	flight.waiters--
	if flight.waiters == 0 {
		flight.cancel()
		if s.assetsFetch == flight {
			s.assetsFetch = nil
		}
	}
	s.assetsMu.Unlock()
}

func (s *Server) serveAssetsZip(w http.ResponseWriter, r *http.Request) {
	if s.serveLocalAssets(w, r) {
		return
	}
	if UpstreamAssetsURL != "" {
		s.waitAssets(r.Context())
		if s.serveLocalAssets(w, r) {
			return
		}
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if fw, err := zw.Create("retroarch-assets.txt"); err == nil {
		_, _ = io.WriteString(fw, "RetroArch marketplace asset bundle placeholder\n")
	}
	_ = zw.Close()

	w.Header().Set("Content-Type", "application/zip")
	http.ServeContent(w, r, "assets.zip", time.Time{}, bytes.NewReader(buf.Bytes()))
}
