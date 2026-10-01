// Command team-ca is the custodian's half of the peer-sharing ceremony
// (docs/sharing.md#the-team-root-and-the-intermediates). It mints
// the team root and signs machine intermediates in exactly the shape proximo's
// config team-root and config intermediate accept. It names no suffix and no
// machine: both are arguments. Run it through team-ca.sh, which needs only
// Docker.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/filippolmt/proximo/internal/config"
	"github.com/filippolmt/proximo/internal/tls"
)

const usage = `usage:
  team-ca root <suffix>
      write team-root.crt and team-root.key (unencrypted: encrypt it, then
      remove the plaintext) into the current directory
  team-ca sign <suffix> <machine> <team-root.crt> <team-root.key> <machine.csr>
      print the machine's intermediate on stdout`

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "team-ca:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	switch {
	case args[0] == "root" && len(args) == 2:
		suffix, err := config.NormalizePeerSuffix(args[1])
		if err != nil {
			return err
		}
		certPEM, keyPEM, err := tls.NewTeamRoot(suffix)
		if err != nil {
			return err
		}
		// O_EXCL: a second run must never overwrite a root already in use.
		if err := writeNew("team-root.key", keyPEM, 0o600); err != nil {
			return err
		}
		if err := writeNew("team-root.crt", certPEM, 0o644); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Wrote team-root.crt and team-root.key for %s.\n", suffix)
		return nil
	case args[0] == "sign" && len(args) == 6:
		suffix, err := config.NormalizePeerSuffix(args[1])
		if err != nil {
			return err
		}
		machine, err := config.NormalizeMachine(args[2])
		if err != nil {
			return err
		}
		var files [3][]byte
		for i, path := range args[3:] {
			if files[i], err = os.ReadFile(path); err != nil {
				return err
			}
		}
		if err := tls.ValidateTeamRoot(files[0], suffix); err != nil {
			return fmt.Errorf("team root %s: %w", args[3], err)
		}
		intPEM, err := tls.SignIntermediate(files[0], files[1], files[2], machine, suffix)
		if err != nil {
			return err
		}
		_, err = stdout.Write(intPEM)
		return err
	}
	return errors.New(usage)
}

func writeNew(path string, data []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
