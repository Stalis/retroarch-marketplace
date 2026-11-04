package catalog

import (
	"archive/zip"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

type assetRoute struct {
	index, redirect, archive, prefix string
	isIndex                          bool
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
				if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == ' ' || c == '-' || c == '_') {
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
		ids := make([]string, 0, len(groups[platform]))
		for _, g := range groups[platform] {
			ids = append(ids, g.id)
		}
		gameLabels := assetLabels(ids)
		entries := make([]string, 0, len(ids))
		for _, g := range groups[platform] {
			name := gameLabels[g.id]
			entry := name + ".zip"
			entries = append(entries, entry)
			routes[base+"/"+url.PathEscape(entry)] = assetRoute{archive: g.id, prefix: name + "/"}
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
	return routes
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
