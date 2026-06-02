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
	var force bool
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Converge the running stack to the installed CLI version",
		Long: "Reconcile the running stack (traefik, dns, watcher) with the " +
			"installed CLI: re-materialize the embedded assets, rebuild the " +
			"in-stack images at the CLI version, and re-pull Traefik. " +
			"Idempotent, never needs sudo, and a soft no-op when Docker or the " +
			"stack is down (it applies on the next `proximo up`).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runUpdate(cmd, force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "rebuild the stack images without using the build cache")
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
// daemon is reachable; stackVer is the running stack version ("" when no stack is
// running); cliVer is the installed CLI version. force overrides the up-to-date
// no-op so `--force` always rebuilds.
func decideUpdate(dockerUp, force bool, stackVer, cliVer string) updateAction {
	switch {
	case !dockerUp:
		return actionDockerDown
	case stackVer == "":
		return actionStackDown
	case stackVer == cliVer && !force:
		return actionUpToDate
	default:
		return actionConverge
	}
}

func runUpdate(cmd *cobra.Command, force bool) error {
	out := cmd.OutOrStdout()
	ctx := context.Background()
	cliVer := version.Version

	dockerUp := checkDocker() == nil
	var stackVer string
	if dockerUp {
		v, err := docker.StackVersion(ctx)
		if err != nil {
			return err
		}
		stackVer = v
	}

	switch decideUpdate(dockerUp, force, stackVer, cliVer) {
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
	fmt.Fprintf(out, "Converging stack %s -> %s...\n", stackVer, cliVer)
	if err := docker.Converge(cfg.TLD, certDir, docker.ConvergeOpts{Force: force}); err != nil {
		return err
	}
	fmt.Fprintf(out, "Stack updated to %s.\n", cliVer)
	return nil
}
