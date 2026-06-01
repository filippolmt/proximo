// Command watcher runs inside the proximo stack and keeps the Traefik container
// attached to the Docker networks of routed containers across their lifecycle.
package main

import (
	"context"
	"log"
	"os/signal"
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
	log.Println("proximo watcher: started")
	if err := w.Run(ctx); err != nil && ctx.Err() == nil {
		log.Fatalf("proximo watcher: %v", err)
	}
}
