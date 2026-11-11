package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"retroarch-marketplace/internal/catalog"
	"retroarch-marketplace/internal/store"
)

func serve(args []string, stderr io.Writer) int {
	cfg, flags, code, ok := parseCommand("serve", "serve [-config path] [-listen address] [-database path] [-packages path] [-scan-interval duration] [-cleanup-interval duration] [-cache-max-age duration] [-boxart-max-distance n]", args, stderr, nil)
	if !ok {
		return code
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "unexpected argument %q\n", flags.Arg(0))
		return 2
	}
	catalog.Version = Version
	log.Printf("starting content-catalog %s on %s", Version, cfg.Listen)
	st, err := store.Open(cfg.Database)
	if err != nil {
		log.Print(err)
		return 1
	}
	defer st.Close()
	server, err := catalog.NewWithBoxArtDistance(st, cfg.Packages, cfg.BoxArtMaxDistance)
	if err != nil {
		log.Print(err)
		return 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go server.RunMaintenance(ctx, cfg.ScanInterval, cfg.CleanupInterval, cfg.CacheMaxAge)
	httpServer := &http.Server{Addr: cfg.Listen, Handler: loggingHandler(server, log.Default()), ReadHeaderTimeout: 5 * time.Second}
	err = httpServer.ListenAndServe()
	cancel()
	log.Print(err)
	return 1
}
