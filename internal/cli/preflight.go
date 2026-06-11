package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/filippolmt/proximo/internal/platform"
	"github.com/moby/moby/client"
)

// preflight verifies that the platform, package manager, and Docker daemon are
// usable before any host changes are made.
func preflight() error {
	if _, err := platform.Current(); err != nil {
		return err
	}
	if _, err := platform.DetectPackageManager(); err != nil {
		return err
	}
	return checkDocker()
}

func checkDocker() error {
	if !platform.Has("docker") {
		return fmt.Errorf("docker is not installed or not on PATH")
	}
	cli, err := client.New(client.FromEnv)
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.Ping(ctx, client.PingOptions{}); err != nil {
		return fmt.Errorf("cannot reach the Docker daemon (is Docker running?): %w", err)
	}
	return nil
}
