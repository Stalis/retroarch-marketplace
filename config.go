package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/BurntSushi/toml"
)

type config struct {
	Listen          string
	Database        string
	Packages        string
	ScanInterval    time.Duration
	CleanupInterval time.Duration
	CacheMaxAge     time.Duration
}

type fileConfig struct {
	Listen          *string `toml:"listen"`
	Database        *string `toml:"database"`
	Packages        *string `toml:"packages"`
	ScanInterval    *string `toml:"scan_interval"`
	CleanupInterval *string `toml:"cleanup_interval"`
	CacheMaxAge     *string `toml:"cache_max_age"`
}

// loadConfig parses cmd's flags (define adds subcommand-specific ones) and merges flags > env > TOML > defaults.
// Callers check positional arguments.
func loadConfig(cmd string, args []string, lookupEnv func(string) (string, bool), define func(*flag.FlagSet)) (config, *flag.FlagSet, error) {
	cfg := config{"127.0.0.1:5050", "catalog.db", "packages", 10 * time.Minute, time.Hour, 24 * time.Hour}
	flags := flag.NewFlagSet("content-catalog "+cmd, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	configPath := flags.String("config", "config.toml", "configuration file")
	listen := flags.String("listen", "", "HTTP listen address")
	database := flags.String("database", "", "SQLite catalog database path")
	packages := flags.String("packages", "", "package cache root")
	scan := flags.String("scan-interval", "", "source scan interval")
	cleanup := flags.String("cleanup-interval", "", "cache cleanup interval")
	maxAge := flags.String("cache-max-age", "", "maximum cache age")
	if define != nil {
		define(flags)
	}
	if err := flags.Parse(args); err != nil {
		return cfg, flags, err
	}
	visited := make(map[string]bool)
	flags.Visit(func(f *flag.Flag) { visited[f.Name] = true })
	path, explicit := lookupEnv("RETRO_CONFIG")
	if !explicit {
		path = "config.toml"
	}
	if visited["config"] {
		path, explicit = *configPath, true
	}
	if explicit && path == "" {
		return cfg, flags, errors.New("config path must not be empty")
	}
	var file fileConfig
	data, err := os.ReadFile(path)
	if err != nil {
		if explicit || !errors.Is(err, os.ErrNotExist) {
			return cfg, flags, fmt.Errorf("reading config %q: %w", path, err)
		}
	} else {
		meta, err := toml.Decode(string(data), &file)
		if err != nil {
			return cfg, flags, fmt.Errorf("decoding config %q: %w", path, err)
		}
		if unknown := meta.Undecoded(); len(unknown) != 0 {
			return cfg, flags, fmt.Errorf("unknown config key %s", unknown[0])
		}
	}
	setString := func(name string, value *string, dst *string) error {
		if value != nil {
			if *value == "" {
				return fmt.Errorf("%s must not be empty", name)
			}
			*dst = *value
		}
		return nil
	}
	setDuration := func(name string, value *string, dst *time.Duration) error {
		if value != nil {
			d, err := time.ParseDuration(*value)
			if err != nil || d <= 0 {
				return fmt.Errorf("%s must be a positive Go duration: %q", name, *value)
			}
			*dst = d
		}
		return nil
	}
	apply := func(values fileConfig) error {
		for _, item := range []struct {
			name  string
			value *string
			dst   *string
		}{
			{"listen", values.Listen, &cfg.Listen}, {"database", values.Database, &cfg.Database}, {"packages", values.Packages, &cfg.Packages},
		} {
			if err := setString(item.name, item.value, item.dst); err != nil {
				return err
			}
		}
		for _, item := range []struct {
			name  string
			value *string
			dst   *time.Duration
		}{
			{"scan_interval", values.ScanInterval, &cfg.ScanInterval}, {"cleanup_interval", values.CleanupInterval, &cfg.CleanupInterval}, {"cache_max_age", values.CacheMaxAge, &cfg.CacheMaxAge},
		} {
			if err := setDuration(item.name, item.value, item.dst); err != nil {
				return err
			}
		}
		return nil
	}
	if err := apply(file); err != nil {
		return cfg, flags, err
	}
	env := func(name string) *string {
		if value, ok := lookupEnv(name); ok {
			return &value
		}
		return nil
	}
	if err := apply(fileConfig{env("RETRO_LISTEN"), env("RETRO_DATABASE"), env("RETRO_PACKAGES"), env("RETRO_SCAN_INTERVAL"), env("RETRO_CLEANUP_INTERVAL"), env("RETRO_CACHE_MAX_AGE")}); err != nil {
		return cfg, flags, err
	}
	selected := func(name string, value *string) *string {
		if visited[name] {
			return value
		}
		return nil
	}
	if err := apply(fileConfig{selected("listen", listen), selected("database", database), selected("packages", packages), selected("scan-interval", scan), selected("cleanup-interval", cleanup), selected("cache-max-age", maxAge)}); err != nil {
		return cfg, flags, err
	}
	return cfg, flags, nil
}
