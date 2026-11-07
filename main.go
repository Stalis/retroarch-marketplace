package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"retroarch-marketplace/internal/catalog"
)

// Version is set at build time via -ldflags.
var Version = "dev"

func main() {
	cfg, err := loadConfig(os.Args[1:], os.LookupEnv)
	if err != nil {
		if err == errVersion {
			fmt.Printf("catalog-server %s\n", Version)
			return
		}
		if err == flag.ErrHelp {
			fmt.Fprintln(os.Stderr, "Usage: catalog-server [-version] [-config path] [-listen address] [-catalog path] [-packages path] [-scan-interval duration] [-cleanup-interval duration] [-cache-max-age duration]")
			return
		}
		log.Fatal(err)
	}
	catalog.Version = Version
	log.Printf("starting catalog-server %s on %s", Version, cfg.Listen)
	if cfg.initDefaultCatalog {
		if err := initDefaultCatalog(cfg.Catalog); err != nil {
			log.Fatal(err)
		}
	}
	server, err := catalog.New(cfg.Catalog, cfg.Packages)
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.RunMaintenance(ctx, cfg.ScanInterval, cfg.CleanupInterval, cfg.CacheMaxAge)
	httpServer := &http.Server{Addr: cfg.Listen, Handler: server, ReadHeaderTimeout: 5 * time.Second}
	err = httpServer.ListenAndServe()
	cancel()
	if err != nil {
		log.Fatal(err)
	}
}

func initDefaultCatalog(path string) error {
	if _, err := os.Lstat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("checking default catalog %q: %w", path, err)
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".catalog-init-*")
	if err != nil {
		return fmt.Errorf("creating default catalog %q: %w", path, err)
	}
	defer os.Remove(f.Name())
	if _, err = f.WriteString(`{"platforms":[],"contents":[]}`); err == nil {
		err = f.Sync()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("writing default catalog %q: %w", path, err)
	}
	if err := os.Link(f.Name(), path); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("publishing default catalog %q: %w", path, err)
	}
	return nil
}
