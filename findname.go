package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"retroarch-marketplace/internal/canonicalnames"
	"retroarch-marketplace/internal/store"
)

var boxartClient = canonicalnames.NewClient()

func findName(args []string, stdout, stderr io.Writer) int {
	var platform, query, file string
	cfg, flags, code, ok := parseCommand("find-name", "find-name [-config path] [-database path] [-boxart-max-distance n] -platform <libretro_name> (-q <title> | -file <path>)", args, stderr, func(f *flag.FlagSet) {
		f.StringVar(&platform, "platform", "", "exact Libretro platform name")
		f.StringVar(&query, "q", "", "game title")
		f.StringVar(&file, "file", "", "ROM filename to match")
	})
	if !ok {
		return code
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "unexpected positional arguments")
		return 2
	}
	if (query == "") == (file == "") {
		fmt.Fprintln(stderr, "exactly one of -q or -file is required")
		return 2
	}
	st, err := store.Open(cfg.Database)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer st.Close()
	finder := canonicalnames.Finder{Store: st, Client: boxartClient, Now: time.Now, MaxTitleDistance: cfg.BoxArtMaxDistance}
	var names []string
	if file != "" {
		names, err = finder.File(context.Background(), platform, file)
	} else {
		names, err = finder.Titles(context.Background(), platform, query)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		if errors.Is(err, canonicalnames.ErrInvalidInput) {
			return 2
		}
		return 1
	}
	if err := json.NewEncoder(stdout).Encode(names); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
