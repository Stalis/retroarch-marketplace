package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"

	"retroarch-marketplace/internal/canonicalnames"
)

func run(args []string, stdout, stderr io.Writer, client *http.Client) int {
	flags := flag.NewFlagSet("find-canonical-name", flag.ContinueOnError)
	flags.SetOutput(stderr)
	platform := flags.String("platform", "", "exact Libretro platform name")
	query := flags.String("q", "", "game title")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "unexpected positional arguments")
		return 2
	}
	names, err := canonicalnames.Lookup(context.Background(), client, *platform, *query)
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

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, canonicalnames.NewClient()))
}
