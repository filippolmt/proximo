package cli

import (
	"context"
	"fmt"

	"github.com/filippolmt/proximo/internal/config"
	"github.com/filippolmt/proximo/internal/docker"
	"github.com/filippolmt/proximo/internal/tls"
	"github.com/filippolmt/proximo/internal/version"
	"github.com/spf13/cobra"
)

func newUpdateCmd() *cobra.Command {
	var (
		force bool
		image string
	)
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Converge the running stack to the installed CLI version",
		Long: "Reconcile the running stack (traefik, dns, watcher, inspector) " +
			"with the installed CLI: re-materialize the embedded assets, pull " +
			"the stack image pinned to the CLI version, and re-pull Traefik. " +
			"Idempotent, never needs sudo, and a soft no-op when Docker or the " +
			"stack is down (it applies on the next `proximo up`).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUpdate(cmd, force, image)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false,
		"pull the stack image even when its tag is already cached")
	imageFlag(cmd, &image)
	return cmd
}

// updateAction is the action `proximo update` takes, derived purely from the
// observed Docker/stack state so the decision can be unit-tested without Docker.
type updateAction int

const (
	actionDockerDown updateAction = iota // Docker unreachable: defer to next `up`
	actionStackDown                      // no stack running: nothing to converge
	actionUpToDate                       // running stack already at the CLI version
	actionConverge                       // rebuild/restart to the CLI version
)

// decideUpdate maps observed state to the update action. dockerUp is whether the
// daemon is reachable; stackRunning is whether any stack container is running;
// stackVer is the running stack version ("" when the stack predates version
// stamping — pre-0.4.0 stacks carry no label); cliVer is the installed CLI
// version; stackImage is the image ref the running stack declares and wantImage
// the one this run would give it. force overrides the up-to-date no-op so
// `--force` always converges.
//
// An unlabeled legacy stack never matches cliVer, so it converges. So does a
// stack whose image differs from the one asked for — including a stack still
// running a sticky --image override that this run is clearing: reporting "up to
// date" while the stack runs something else is the defect, not the shortcut.
func decideUpdate(dockerUp, stackRunning, force bool, stackVer, cliVer, stackImage, wantImage string) updateAction {
	switch {
	case !dockerUp:
		return actionDockerDown
	case !stackRunning:
		return actionStackDown
	case stackVer == cliVer && stackImage == wantImage && !force:
		return actionUpToDate
	default:
		return actionConverge
	}
}

func runUpdate(cmd *cobra.Command, force bool, image string) error {
	out := cmd.OutOrStdout()
	ctx := context.Background()
	cliVer := version.Version

	dockerUp := checkDocker() == nil
	var stack docker.StackInfo
	if dockerUp {
		var err error
		if stack, err = docker.StackStatus(ctx); err != nil {
			return err
		}
	}
	opts := docker.ConvergeOpts{Force: force, Image: image}
	wantImage := opts.EffectiveImage()

	// An explicit --image always converges, even onto a stack already running
	// that ref: while an override is in effect `update` must never claim the
	// stack is up to date with the CLI, because it is not running the CLI's image.
	switch decideUpdate(dockerUp, stack.Running, force || image != "", stack.Version, cliVer, stack.Image, wantImage) {
	case actionDockerDown:
		fmt.Fprintln(out, "Docker is not reachable; the update will apply on the next `proximo up`.")
		return nil
	case actionStackDown:
		fmt.Fprintln(out, "No proximo stack is running; nothing to converge. The update will apply on the next `proximo up`.")
		return nil
	case actionUpToDate:
		fmt.Fprintf(out, "Stack is up to date (%s).\n", cliVer)
		return nil
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	certDir, err := tls.Dir()
	if err != nil {
		return err
	}
	reportImage(out, opts)
	fmt.Fprintf(out, "Converging stack %s -> %s...\n", docker.DisplayVersion(stack.Version), cliVer)
	if err := docker.Converge(cfg.TLD, certDir, opts); err != nil {
		return err
	}
	fmt.Fprintf(out, "Stack updated to %s.\n", cliVer)
	return nil
}
