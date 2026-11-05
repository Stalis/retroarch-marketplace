package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

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
	server, err := catalog.New(cfg.Catalog, cfg.Packages)
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.RunMaintenance(ctx, cfg.ScanInterval, cfg.CleanupInterval, cfg.CacheMaxAge)
	err = http.ListenAndServe(cfg.Listen, server)
	cancel()
	if err != nil {
		log.Fatal(err)
	}
}
