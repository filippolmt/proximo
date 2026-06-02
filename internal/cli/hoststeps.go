package cli

import (
	"fmt"
	"io"

	"github.com/filippolmt/proximo/internal/dns"
	"github.com/filippolmt/proximo/internal/platform"
	"github.com/filippolmt/proximo/internal/tls"
)

// defaultRunner is the production privileged-exec runner the host commands use.
// Tests drive the step sequencer with a fake Runner instead.
var defaultRunner platform.Runner = platform.ExecRunner{}

// hostStep is one reversible host mutation. apply performs it; revert undoes it.
// applyMsg/revertMsg are the user-facing "==>" progress lines printed before the
// respective action (empty means the step is grouped under the previous line's
// banner, reproducing today's "system + NSS" trust grouping).
type hostStep struct {
	applyMsg  string
	revertMsg string
	apply     func() error
	revert    func() error
}

// hostSteps is the single source of truth for the host mutations install
// applies and uninstall reverses, in install order: CA, resolver, system trust,
// NSS trust. install applies the list forward; uninstall reverts it in reverse,
// so a mutation can never be applied without a matching reversal. Privileged
// exec is routed through r so the sequence is testable with a fake Runner.
func hostSteps(r platform.Runner, tld string) []hostStep {
	return []hostStep{
		{
			applyMsg: "==> Generating local CA",
			apply:    func() error { _, _, err := tls.EnsureCA(); return err },
			revert:   func() error { return tls.Purge() },
		},
		{
			applyMsg:  fmt.Sprintf("==> Configuring host resolver for .%s", tld),
			revertMsg: fmt.Sprintf("==> Removing host resolver for .%s", tld),
			apply:     func() error { return dns.ConfigureResolver(r, tld) },
			revert:    func() error { return dns.RemoveResolver(r, tld) },
		},
		{
			// The trust banner is split across the system+NSS pair so it prints
			// once on the way in (before the first apply) and once on the way out
			// (before the first revert, which in reverse order is NSS).
			applyMsg: "==> Installing CA trust (system + NSS)",
			apply:    func() error { return tls.InstallSystemTrust(r) },
			revert:   func() error { return tls.RemoveSystemTrust(r) },
		},
		{
			revertMsg: "==> Removing CA trust (system + NSS)",
			apply:     func() error { return tls.InstallNSSTrust(r) },
			revert:    func() error { return tls.RemoveNSSTrust(r) },
		},
	}
}

// applySteps applies steps in order, printing each applyMsg first. When a step
// fails, the already-applied prefix is reverted in reverse (best-effort) so a
// mid-install failure leaves the host in its pre-install state, and the original
// error is returned.
func applySteps(out io.Writer, steps []hostStep) error {
	for i, s := range steps {
		if s.applyMsg != "" {
			fmt.Fprintln(out, s.applyMsg)
		}
		if err := s.apply(); err != nil {
			for j := i - 1; j >= 0; j-- {
				_ = steps[j].revert()
			}
			return err
		}
	}
	return nil
}

// revertSteps reverts steps in reverse order (full uninstall), printing each
// revertMsg first. It returns the first revert error, matching the prior
// uninstall's fail-fast behavior.
func revertSteps(out io.Writer, steps []hostStep) error {
	for i := len(steps) - 1; i >= 0; i-- {
		s := steps[i]
		if s.revertMsg != "" {
			fmt.Fprintln(out, s.revertMsg)
		}
		if err := s.revert(); err != nil {
			return err
		}
	}
	return nil
}
