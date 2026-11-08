package catalog

import (
	"encoding/json"
	"encoding/xml"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const maxBoxartIndex = 8 << 20

var boxartClient = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

func serveCanonicalNames(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	platform, query := r.URL.Query().Get("platform"), strings.TrimSpace(r.URL.Query().Get("q"))
	if strings.TrimSpace(platform) == "" || query == "" || platform == "." || platform == ".." || strings.ContainsAny(platform, "/\\:") || strings.IndexFunc(platform, unicode.IsControl) >= 0 {
		http.Error(w, "invalid platform or query", http.StatusBadRequest)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, "https://thumbnails.libretro.com/"+url.PathEscape(platform)+"/Named_Boxarts/", nil)
	if err != nil {
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	resp, err := boxartClient.Do(req)
	if err != nil {
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		http.Error(w, "upstream error", http.StatusBadGateway)
		return
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBoxartIndex+1))
	if err != nil || len(data) > maxBoxartIndex || !utf8.Valid(data) || !strings.Contains(strings.ToLower(string(data)), "</html>") {
		http.Error(w, "invalid upstream listing", http.StatusBadGateway)
		return
	}
	decoder := xml.NewDecoder(strings.NewReader(string(data)))
	decoder.Strict = false
	decoder.AutoClose = xml.HTMLAutoClose
	decoder.Entity = xml.HTMLEntity
	names := []string{}
	seen := map[string]bool{}
	htmlOpen, htmlClose := false, false
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			http.Error(w, "invalid upstream listing", http.StatusBadGateway)
			return
		}
		switch tag := token.(type) {
		case xml.StartElement:
			if strings.EqualFold(tag.Name.Local, "html") {
				htmlOpen = true
			}
			if !strings.EqualFold(tag.Name.Local, "a") {
				continue
			}
			for _, attr := range tag.Attr {
				if !strings.EqualFold(attr.Name.Local, "href") {
					continue
				}
				name, err := url.PathUnescape(attr.Value)
				if err != nil {
					http.Error(w, "invalid upstream listing", http.StatusBadGateway)
					return
				}
				if strings.ContainsAny(name, "/\\") || !strings.HasSuffix(strings.ToLower(name), ".png") {
					continue
				}
				name = name[:len(name)-4]
				if name != "" && !seen[name] {
					names = append(names, name)
					seen[name] = true
				}
			}
		case xml.EndElement:
			if strings.EqualFold(tag.Name.Local, "html") {
				htmlClose = true
			}
		}
	}
	if !htmlOpen || !htmlClose {
		http.Error(w, "invalid upstream listing", http.StatusBadGateway)
		return
	}
	type candidate struct {
		name, lower string
		equal       bool
		distance    int
	}
	q := strings.ToLower(query)
	qt := tokenSet(q)
	ranked := make([]candidate, 0, len(names))
	for _, name := range names {
		lower := strings.ToLower(name)
		ranked = append(ranked, candidate{name, lower, tokenSet(lower) == qt, runeDistance(q, lower)})
	}
	sort.Slice(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if a.equal != b.equal {
			return a.equal
		}
		if a.distance != b.distance {
			return a.distance < b.distance
		}
		if a.lower != b.lower {
			return a.lower < b.lower
		}
		return a.name < b.name
	})
	result := []string{}
	for i, item := range ranked {
		if i == 10 {
			break
		}
		result = append(result, item.name)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

func tokenSet(s string) string {
	set := map[string]bool{}
	for _, token := range strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }) {
		set[token] = true
	}
	tokens := make([]string, 0, len(set))
	for token := range set {
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)
	return strings.Join(tokens, "\x00")
}

func runeDistance(a, b string) int {
	x, y := []rune(a), []rune(b)
	row := make([]int, len(y)+1)
	for j := range row {
		row[j] = j
	}
	for i, r := range x {
		prev := row[0]
		row[0] = i + 1
		for j, s := range y {
			old := row[j+1]
			cost := 0
			if r != s {
				cost = 1
			}
			row[j+1] = min(row[j+1]+1, row[j]+1, prev+cost)
			prev = old
		}
	}
	return row[len(y)]
}
