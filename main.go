package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

// Version is set at build time via -ldflags.
var Version = "dev"

const usage = `Usage: content-catalog <command> [flags]

Commands:
  serve      run the catalog HTTP server
  scan       add games found under a directory to the database
  find-name  look up Libretro canonical names
  version    print the version
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "-h", "--help", "help":
		fmt.Fprint(stderr, usage)
		return 0
	case "serve":
		return serve(args[1:], stderr)
	case "scan":
		return scan(args[1:], stdout, stderr)
	case "find-name":
		return findName(args[1:], stdout, stderr)
	case "version":
		fmt.Fprintf(stdout, "content-catalog %s\n", Version)
		return 0
	}
	fmt.Fprintf(stderr, "unknown command %q\n%s", args[0], usage)
	return 2
}

// parseCommand loads the configuration for cmd; ok is false when the caller must exit with code.
func parseCommand(cmd, synopsis string, args []string, stderr io.Writer, define func(*flag.FlagSet)) (cfg config, flags *flag.FlagSet, code int, ok bool) {
	cfg, flags, err := loadConfig(cmd, args, os.LookupEnv, define)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Fprintf(stderr, "Usage: content-catalog %s\n\nFlags:\n", synopsis)
		flags.SetOutput(stderr)
		flags.PrintDefaults()
		return cfg, flags, 0, false
	}
	if err != nil {
		fmt.Fprintf(stderr, "content-catalog %s: %v\n", cmd, err)
		return cfg, flags, 2, false
	}
	return cfg, flags, 0, true
}
