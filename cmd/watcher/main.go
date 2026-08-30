// Command watcher runs inside the proximo stack and keeps the Traefik container
// attached to the Docker networks of routed containers across their lifecycle.
package main

import (
	"context"
	"log"
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
	log.Println("proximo watcher: started")
	if err := w.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatalf("proximo watcher: %v", err)
	}
}
