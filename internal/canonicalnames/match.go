package canonicalnames

import (
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	discMarker = regexp.MustCompile(`(?i)\s*(?:\((?:disc|disk|cd)\s*(\d+)(?:\s*of\s*\d+)?\)|[ _.-](?:disc|disk|cd)[ _]?(\d+)$)`)
	tagGroup   = regexp.MustCompile(`\([^)]*\)|\[[^\]]*\]`)
	roman      = map[string]string{"ii": "2", "iii": "3", "iv": "4", "v": "5", "vi": "6", "vii": "7", "viii": "8", "ix": "9", "x": "10"}
)

func stem(filename string) string {
	base := filepath.Base(filepath.FromSlash(filename))
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// Title is the filename without directory, extension and disc marker.
func Title(filename string) string {
	return strings.TrimSpace(discMarker.ReplaceAllString(stem(filename), ""))
}

// DiscNumber returns the disc number from a "(Disc N)" or "_disc N" style marker.
func DiscNumber(filename string) (int, bool) {
	m := discMarker.FindStringSubmatch(stem(filename))
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[1] + m[2])
	return n, err == nil
}

// words splits s into runs of letters and runs of digits.
func words(s string) []string {
	var out []string
	start, kind := -1, 0
	for i, r := range s + " " {
		k := 0
		if unicode.IsLetter(r) {
			k = 1
		} else if unicode.IsDigit(r) {
			k = 2
		}
		if k != kind {
			if kind != 0 {
				out = append(out, s[start:i])
			}
			start, kind = i, k
		}
	}
	return out
}

// Tokens is the normalized word set of a title, ignoring (...) and [...] tags.
func Tokens(s string) map[string]bool {
	set := map[string]bool{}
	for _, w := range words(strings.ToLower(tagGroup.ReplaceAllString(s, " "))) {
		if n, ok := roman[w]; ok {
			w = n
		}
		set[w] = true
	}
	return set
}

func tagWords(s string) map[string]bool {
	set := map[string]bool{}
	for _, tag := range tagGroup.FindAllString(s, -1) {
		for _, w := range words(strings.ToLower(tag)) {
			set[w] = true
		}
	}
	return set
}

var regions = []struct {
	name    string
	aliases []string
}{
	{"usa", []string{"u", "us", "usa"}},
	{"europe", []string{"e", "eu", "eur", "europe"}},
	{"japan", []string{"j", "jp", "jpn", "japan"}},
}

// regionRank prefers the file's own region, then USA, Europe, Japan and others.
func regionRank(name string, preferred string) int {
	tags := tagWords(name)
	if preferred != "" && tags[preferred] {
		return 0
	}
	for i, r := range regions {
		if tags[r.name] {
			return i + 1
		}
	}
	return len(regions) + 1
}

// MatchFile returns names whose title words overlap the filename's by a Jaccard score of at least 0.6, best first, at most 10.
func MatchFile(filename string, names []string) []string {
	want := Tokens(Title(filename))
	fileTags := tagWords(stem(filename))
	preferred := ""
	for _, r := range regions {
		for _, a := range r.aliases {
			if preferred == "" && fileTags[a] {
				preferred = r.name
			}
		}
	}
	type candidate struct {
		name   string
		score  float64
		region int
	}
	var ranked []candidate
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		have := Tokens(name)
		common := 0
		for w := range want {
			if have[w] {
				common++
			}
		}
		union := len(want) + len(have) - common
		if union == 0 {
			continue
		}
		if score := float64(common) / float64(union); score >= 0.6 {
			ranked = append(ranked, candidate{name, score, regionRank(name, preferred)})
		}
	}
	sort.Slice(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if a.region != b.region {
			return a.region < b.region
		}
		if la, lb := utf8.RuneCountInString(a.name), utf8.RuneCountInString(b.name); la != lb {
			return la < lb
		}
		return a.name < b.name
	})
	result := []string{}
	for i := 0; i < len(ranked) && i < 10; i++ {
		result = append(result, ranked[i].name)
	}
	return result
}
