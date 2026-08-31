// Command watcher runs inside the proximo stack and keeps the Traefik container
// attached to the Docker networks of routed containers across their lifecycle.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/filippolmt/proximo/internal/docker"
	"github.com/filippolmt/proximo/internal/version"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	w, err := docker.NewWatcher()
	if err != nil {
		log.Fatalf("proximo watcher: %v", err)
	}
	// Build identity, stamped by the same -ldflags the CLI carries — useful for
	// spotting a stale mobile-tag (:main) image. It is a debug aid only; skew
	// detection uses the proximo.version service label.
	log.Printf("proximo watcher: build %s (%s)", version.Version, version.Commit)
	// The Incident read API, on a loopback-published port: `proximo errors` asks
	// the watcher what the runtime declared, the way it asks the hop what the
	// browser reported.
	apiAddr := getenv("PROXIMO_WATCHER_API_ADDR", ":9002")
	go func() {
		log.Printf("proximo watcher: Incident read API on %s", apiAddr)
		log.Fatal(http.ListenAndServe(apiAddr, docker.IncidentAPI{Store: w.Incidents()}))
	}()

	log.Println("proximo watcher: started")
	if err := w.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatalf("proximo watcher: %v", err)
	}
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
