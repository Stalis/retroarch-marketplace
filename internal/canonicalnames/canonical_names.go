package canonicalnames

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
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

var ErrInvalidInput = errors.New("invalid platform or query")

func NewClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func Lookup(ctx context.Context, client *http.Client, platform, query string) ([]string, error) {
	query = strings.TrimSpace(query)
	if strings.TrimSpace(platform) == "" || query == "" || platform == "." || platform == ".." || strings.ContainsAny(platform, "/\\:") || strings.IndexFunc(platform, unicode.IsControl) >= 0 {
		return nil, ErrInvalidInput
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://thumbnails.libretro.com/"+url.PathEscape(platform)+"/Named_Boxarts/", nil)
	if err != nil {
		return nil, fmt.Errorf("upstream error: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upstream error: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("upstream status: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBoxartIndex+1))
	if err != nil || len(data) > maxBoxartIndex || !utf8.Valid(data) || !strings.Contains(strings.ToLower(string(data)), "</html>") {
		return nil, errors.New("invalid upstream listing")
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
			return nil, errors.New("invalid upstream listing")
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
					return nil, errors.New("invalid upstream listing")
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
		return nil, errors.New("invalid upstream listing")
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
	return result, nil
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
