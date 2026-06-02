// Command watcher runs inside the proximo stack and keeps the Traefik container
// attached to the Docker networks of routed containers across their lifecycle.
package main

import (
	"context"
	"log"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/filippolmt/proximo/internal/docker"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	w, err := docker.NewWatcher()
	if err != nil {
		log.Fatalf("proximo watcher: %v", err)
	}
	// The binary's own module version (recorded by `go install <module>@<ref>`)
	// is the most truthful build identity — useful for spotting a stale
	// mobile-ref (@main) build cache. It is a debug aid only; skew detection uses
	// the proximo.version service label.
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		log.Printf("proximo watcher: build %s", info.Main.Version)
	}
	log.Println("proximo watcher: started")
	if err := w.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatalf("proximo watcher: %v", err)
	}
}
