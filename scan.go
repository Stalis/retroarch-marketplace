package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"retroarch-marketplace/internal/canonicalnames"
	"retroarch-marketplace/internal/content"
	"retroarch-marketplace/internal/store"
)

var (
	skippedExtensions = map[string]bool{".txt": true, ".nfo": true, ".md": true, ".pdf": true, ".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".bmp": true,
		".sav": true, ".srm": true, ".state": true, ".cfg": true, ".ini": true, ".db": true, ".db-wal": true, ".db-shm": true, ".db-journal": true, ".json": true, ".m3u": true, ".xml": true, ".dat": true, ".log": true}
	cueFileLine = regexp.MustCompile(`(?i)^\s*FILE\s+(?:"([^"]*)"|(\S+))\s+\S+\s*$`)
	nonSlug     = regexp.MustCompile(`[^a-z0-9]+`)
)

type scanAdded struct {
	ID            string   `json:"id"`
	CanonicalName string   `json:"canonical_name"`
	Platform      string   `json:"platform"`
	NameSource    string   `json:"name_source"`
	Files         []string `json:"files"`
}

type scanSkipped struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type scanResult struct {
	Added   []scanAdded   `json:"added"`
	Skipped []scanSkipped `json:"skipped"`
}

// scanDisc is one disc found on disk; sources are slash paths relative to the database directory.
type scanDisc struct {
	dir      string // source directory of the entrypoint
	name     string // entrypoint base name
	platform content.Platform
	disc     content.Disc
}

func slug(name string) string {
	if id := strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(name), "-"), "-"); id != "" {
		return id
	}
	return "game"
}

// within returns the slash path of target relative to base when target is inside base.
func within(base, target string) (string, bool) {
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// cueReferences returns the FILE names of a cue sheet.
func cueReferences(filename string) ([]string, error) {
	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var refs []string
	lines := bufio.NewScanner(io.LimitReader(f, 1<<20))
	for lines.Scan() {
		if m := cueFileLine.FindStringSubmatch(strings.TrimPrefix(lines.Text(), "\ufeff")); m != nil {
			refs = append(refs, m[1]+m[2])
		}
	}
	return refs, lines.Err()
}

func scan(args []string, stdout, stderr io.Writer) int {
	var platformFlag string
	var dryRun, offline bool
	cfg, flags, code, ok := parseCommand("scan", "scan [-config path] [-database path] [-platform <libretro_name>] [-dry-run] [-offline] <dir>", args, stderr, func(f *flag.FlagSet) {
		f.StringVar(&platformFlag, "platform", "", "Libretro platform name for every file (default: detect from directory names)")
		f.BoolVar(&dryRun, "dry-run", false, "report what would be added without writing")
		f.BoolVar(&offline, "offline", false, "use filenames as canonical names without BoxArt lookups")
	})
	if !ok {
		return code
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "scan requires exactly one directory argument")
		return 2
	}
	st, err := store.Open(cfg.Database)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer st.Close()
	result, err := scanDirectory(context.Background(), st, cfg.Database, flags.Arg(0), platformFlag, dryRun, offline, stderr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func scanDirectory(ctx context.Context, st *store.Store, database, dir, platformFlag string, dryRun, offline bool, stderr io.Writer) (*scanResult, error) {
	result := &scanResult{Added: []scanAdded{}, Skipped: []scanSkipped{}}
	database, err := filepath.Abs(database)
	if err != nil {
		return nil, err
	}
	database, err = filepath.EvalSymlinks(database)
	if err != nil {
		return nil, err
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	// Sources stay relative to the database directory, so the scan root must sit inside it,
	// lexically or after resolving symlinks.
	relDir, inside := within(st.Root(), absDir)
	walkRoot, err := filepath.EvalSymlinks(absDir)
	if err != nil {
		return nil, err
	}
	if !inside {
		if realRoot, err := filepath.EvalSymlinks(st.Root()); err == nil {
			relDir, inside = within(realRoot, walkRoot)
		}
	}
	if !inside {
		return nil, fmt.Errorf("scan directory %q must be inside the database directory %q", absDir, st.Root())
	}
	source := func(file string) string {
		rel, _ := within(walkRoot, file)
		return path.Clean(path.Join(relDir, rel))
	}

	// Platforms by libretro_name: database first, then the built-in list.
	existing, err := st.Platforms(ctx)
	if err != nil {
		return nil, err
	}
	platforms := map[string]content.Platform{}
	usedPlatformIDs := map[string]bool{}
	for _, p := range existing {
		usedPlatformIDs[p.ID] = true
		if _, ok := platforms[p.LibretroName]; !ok {
			platforms[p.LibretroName] = p.Platform
		}
	}
	known := map[string]content.Platform{}
	for _, p := range store.KnownPlatforms() {
		known[p.LibretroName] = p
	}
	var newPlatforms []content.Platform
	resolvePlatform := func(libretro string) (content.Platform, bool) {
		if p, ok := platforms[libretro]; ok {
			return p, true
		}
		p, ok := known[libretro]
		if !ok {
			return p, false
		}
		base := p.ID
		for n := 2; usedPlatformIDs[p.ID]; n++ {
			p.ID = base + "-" + strconv.Itoa(n)
		}
		usedPlatformIDs[p.ID] = true
		platforms[libretro] = p
		newPlatforms = append(newPlatforms, p)
		return p, true
	}
	if platformFlag != "" {
		if _, ok := resolvePlatform(platformFlag); !ok {
			return nil, fmt.Errorf("unknown platform %q: add it in the admin UI first", platformFlag)
		}
	}
	platformFor := func(src string) (content.Platform, bool) {
		if platformFlag != "" {
			return resolvePlatform(platformFlag)
		}
		for _, component := range strings.Split(path.Dir(src), "/") {
			if p, ok := resolvePlatform(component); ok {
				return p, true
			}
		}
		return content.Platform{}, false
	}

	var files []string
	err = filepath.WalkDir(walkRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == walkRoot {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || skippedExtensions[strings.ToLower(filepath.Ext(d.Name()))] {
			return nil
		}
		actual := p
		if d.Type()&os.ModeSymlink != 0 {
			if resolved, err := filepath.EvalSymlinks(p); err == nil {
				actual = resolved
			}
		}
		if actual == database || actual == database+"-wal" || actual == database+"-shm" || actual == database+"-journal" {
			return nil
		}
		// Symlinked directories are not descended into; symlinked regular files count.
		if info, err := os.Stat(p); err == nil && info.Mode().IsRegular() {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)

	consumed := map[string]bool{}
	var discs []scanDisc
	addDisc := func(entry string, disc content.Disc) {
		src := source(entry)
		p, ok := platformFor(src)
		if !ok {
			result.Skipped = append(result.Skipped, scanSkipped{src, "no platform"})
			return
		}
		discs = append(discs, scanDisc{dir: path.Dir(src), name: path.Base(src), platform: p, disc: disc})
	}
	for _, file := range files {
		if !strings.EqualFold(filepath.Ext(file), ".cue") {
			continue
		}
		consumed[file] = true
		refs, err := cueReferences(file)
		if err != nil {
			result.Skipped = append(result.Skipped, scanSkipped{source(file), err.Error()})
			continue
		}
		disc := content.Disc{Entrypoint: filepath.Base(file), Files: []content.File{{Source: source(file), Path: filepath.Base(file)}}}
		reason := ""
		for _, ref := range refs {
			target := filepath.Join(filepath.Dir(file), filepath.FromSlash(ref))
			_, inside := within(walkRoot, target)
			info, err := os.Stat(target)
			if !inside || err != nil || !info.Mode().IsRegular() {
				if reason == "" {
					reason = "cue reference missing: " + ref
				}
				continue
			}
			consumed[target] = true
			disc.Files = append(disc.Files, content.File{Source: source(target), Path: path.Clean(filepath.ToSlash(ref))})
		}
		if reason != "" {
			result.Skipped = append(result.Skipped, scanSkipped{source(file), reason})
			continue
		}
		addDisc(file, disc)
	}
	for _, file := range files {
		if !consumed[file] {
			addDisc(file, content.Disc{Entrypoint: filepath.Base(file), Files: []content.File{{Source: source(file), Path: filepath.Base(file)}}})
		}
	}

	// Discs of one title in one directory with disc numbers form a multi-disc game.
	type groupKey struct{ dir, platform, title string }
	groups := map[groupKey][]scanDisc{}
	var order []groupKey
	for i, d := range discs {
		key := groupKey{d.dir, d.platform.ID, canonicalnames.Title(d.name)}
		if _, numbered := canonicalnames.DiscNumber(d.name); !numbered {
			key.title = "\x00" + strconv.Itoa(i) // never grouped
		}
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], d)
	}

	existingIDs := map[string]bool{}
	summaries, err := st.Games(ctx, store.Filter{Count: -1})
	if err != nil {
		return nil, err
	}
	for _, g := range summaries {
		existingIDs[g.ID] = true
	}
	finder := canonicalnames.Finder{Store: st, Client: boxartClient, Now: time.Now}
	failedLookups := map[string]bool{}
	var games []content.Game
	for _, key := range order {
		group := groups[key]
		sort.SliceStable(group, func(i, j int) bool {
			a, _ := canonicalnames.DiscNumber(group[i].name)
			b, _ := canonicalnames.DiscNumber(group[j].name)
			return a < b
		})
		first := group[0]
		entry := path.Join(first.dir, first.name)
		present := false
		var sources []string
		for _, d := range group {
			for _, f := range d.disc.Files {
				sources = append(sources, f.Source)
				if !present {
					if present, err = st.HasSource(ctx, f.Source); err != nil {
						return nil, err
					}
				}
			}
		}
		if present {
			result.Skipped = append(result.Skipped, scanSkipped{entry, "already in database"})
			continue
		}
		name, nameSource := canonicalnames.Title(first.name), "filename"
		libretro := first.platform.LibretroName
		if !offline && !failedLookups[libretro] {
			matches, err := finder.File(ctx, libretro, first.name)
			if err != nil {
				fmt.Fprintf(stderr, "canonical name lookup for %q: %v\n", entry, err)
				failedLookups[libretro] = true
			} else if len(matches) > 0 {
				name, nameSource = matches[0], "boxart"
			}
		}
		id := slug(name)
		for n := 2; existingIDs[id]; n++ {
			id = slug(name) + "-" + strconv.Itoa(n)
		}
		g := content.Game{ID: id, CanonicalName: name, PlatformID: first.platform.ID}
		for _, d := range group {
			g.Discs = append(g.Discs, d.disc)
		}
		if len(group) > 1 {
			playlist := canonicalnames.Title(first.name) + ".m3u"
			if !content.SafePath(playlist, false) {
				playlist = id + ".m3u"
			}
			g.Playlist = &playlist
		}
		if err := g.Validate(); err != nil {
			result.Skipped = append(result.Skipped, scanSkipped{entry, err.Error()})
			continue
		}
		existingIDs[id] = true
		games = append(games, g)
		result.Added = append(result.Added, scanAdded{ID: id, CanonicalName: name, Platform: libretro, NameSource: nameSource, Files: sources})
	}
	if !dryRun && len(games) > 0 {
		used := map[string]bool{}
		for _, g := range games {
			used[g.PlatformID] = true
		}
		var create []content.Platform
		for _, p := range newPlatforms {
			if used[p.ID] {
				create = append(create, p)
			}
		}
		if err := st.CreateGames(ctx, games, create); err != nil {
			return nil, fmt.Errorf("writing games: %w", err)
		}
	}
	return result, nil
}
