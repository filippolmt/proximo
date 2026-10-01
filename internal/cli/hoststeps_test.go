package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/filippolmt/proximo/internal/config"
	"github.com/filippolmt/proximo/internal/platform"
)

// recordRunner is a fake platform.Runner that records the host calls steps make,
// so step ordering and rollback are assertable without touching the host.
type recordRunner struct{ calls []string }

func (r *recordRunner) Run(name string, args ...string) error {
	r.calls = append(r.calls, strings.TrimSpace(name+" "+strings.Join(args, " ")))
	return nil
}
func (r *recordRunner) Sudo(args ...string) error {
	r.calls = append(r.calls, "sudo "+strings.Join(args, " "))
	return nil
}
func (r *recordRunner) WriteFilePrivileged(path string, _ []byte, _ os.FileMode) error {
	r.calls = append(r.calls, "write "+path)
	return nil
}
func (r *recordRunner) RemoveFilePrivileged(path string) error {
	r.calls = append(r.calls, "remove "+path)
	return nil
}

// recStep is a fake host step that records its apply/revert through the fake
// runner; failApply makes apply return an error.
func recStep(r *recordRunner, name string, failApply bool) hostStep {
	return hostStep{
		apply: func() error {
			_ = r.Run("apply", name)
			if failApply {
				return fmt.Errorf("apply %s failed", name)
			}
			return nil
		},
		revert: func() error { _ = r.Run("revert", name); return nil },
	}
}

func TestApplyStepsRunsInOrder(t *testing.T) {
	r := &recordRunner{}
	steps := []hostStep{recStep(r, "a", false), recStep(r, "b", false), recStep(r, "c", false)}
	if err := applySteps(io.Discard, steps); err != nil {
		t.Fatalf("applySteps: %v", err)
	}
	want := []string{"apply a", "apply b", "apply c"}
	if !slices.Equal(r.calls, want) {
		t.Errorf("apply order = %v, want %v", r.calls, want)
	}
}

func TestRevertStepsRunsInReverse(t *testing.T) {
	r := &recordRunner{}
	steps := []hostStep{recStep(r, "a", false), recStep(r, "b", false), recStep(r, "c", false)}
	if err := revertSteps(io.Discard, steps); err != nil {
		t.Fatalf("revertSteps: %v", err)
	}
	want := []string{"revert c", "revert b", "revert a"}
	if !slices.Equal(r.calls, want) {
		t.Errorf("revert order = %v, want %v", r.calls, want)
	}
}

func TestApplyStepsRollsBackAppliedPrefixOnFailure(t *testing.T) {
	r := &recordRunner{}
	// b fails mid-apply; a (the applied prefix) must be reverted, c never starts.
	steps := []hostStep{recStep(r, "a", false), recStep(r, "b", true), recStep(r, "c", false)}
	err := applySteps(io.Discard, steps)
	if err == nil {
		t.Fatal("applySteps should return the failing step's error")
	}
	want := []string{"apply a", "apply b", "revert a"}
	if !slices.Equal(r.calls, want) {
		t.Errorf("rollback sequence = %v, want %v", r.calls, want)
	}
}

// TestHostStepsOrderAndBanners pins the single source of truth: install order
// (CA, resolver, system trust, NSS trust) and the exact progress banners, so the
// install output stays byte-identical and uninstall (reverse) prints the trust
// and resolver lines in inverse order.
func TestHostStepsOrderAndBanners(t *testing.T) {
	cfg := config.Default()
	steps := hostSteps(platform.ExecRunner{}, &cfg)
	if len(steps) != 4 {
		t.Fatalf("hostSteps len = %d, want 4", len(steps))
	}
	wantApply := []string{
		"==> Generating local CA",
		"==> Configuring host resolver for .test",
		"==> Installing CA trust (system + NSS)",
		"",
	}
	wantRevert := []string{
		"",
		"==> Removing host resolver for .test",
		"",
		"==> Removing CA trust (system + NSS)",
	}
	for i, s := range steps {
		if s.applyMsg != wantApply[i] {
			t.Errorf("step %d applyMsg = %q, want %q", i, s.applyMsg, wantApply[i])
		}
		if s.revertMsg != wantRevert[i] {
			t.Errorf("step %d revertMsg = %q, want %q", i, s.revertMsg, wantRevert[i])
		}
		if s.apply == nil || s.revert == nil {
			t.Errorf("step %d has a nil apply/revert", i)
		}
	}
}

func TestHostStepsTeamRoot(t *testing.T) {
	calls := stubTrust(t)
	cfg, fp := configureTeamRoot(t)
	steps := hostSteps(defaultRunner, &cfg)
	if len(steps) != 5 {
		t.Fatalf("hostSteps len = %d, want 5", len(steps))
	}
	// The local CA's steps are the same, configured or not (constraint 9).
	unset := config.Default()
	for i, s := range hostSteps(defaultRunner, &unset) {
		if steps[i].applyMsg != s.applyMsg || steps[i].revertMsg != s.revertMsg {
			t.Errorf("step %d banners changed with a team root configured", i)
		}
	}
	last := steps[4]
	if last.applyMsg != teamRootApplyMsg || last.revertMsg != teamRootRevertMsg {
		t.Errorf("team root step banners = %q / %q", last.applyMsg, last.revertMsg)
	}
	if err := last.apply(); err != nil {
		t.Fatal(err)
	}
	if cfg.TeamRootTrusted != fp {
		t.Errorf("install did not record the trusted fingerprint: %q", cfg.TeamRootTrusted)
	}
	if err := last.revert(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"team-root " + cfg.TeamRoot, "remove team-root " + fp}; !slices.Equal(*calls, want) {
		t.Errorf("calls = %v, want %v", *calls, want)
	}

	// A peer-side failure never fails the command that serves .test: install
	// warns and goes on, and uninstall still reaches the local CA.
	installTeamRootTrust = func(platform.Runner, string) error { return errors.New("boom") }
	removeTeamRootTrust = func(platform.Runner, string) error { return errors.New("boom") }
	if err := last.apply(); err != nil {
		t.Errorf("a team root failure failed install: %v", err)
	}
	if err := last.revert(); err != nil {
		t.Errorf("a team root failure failed uninstall: %v", err)
	}
}

// uninstall removes what was trusted, even once team-root no longer names it,
// and touches no store for a root that was configured but never trusted.
func TestHostStepsTeamRootRevertFollowsWhatWasTrusted(t *testing.T) {
	calls := stubTrust(t)
	cfg := config.Default()
	cfg.TeamRootTrusted = "ABCD"
	steps := hostSteps(defaultRunner, &cfg)
	if len(steps) != 5 {
		t.Fatalf("hostSteps len = %d, want 5", len(steps))
	}
	if err := steps[4].revert(); err != nil {
		t.Fatal(err)
	}
	cfg = config.Default()
	cfg.TeamRoot = "/never/trusted.crt"
	if err := hostSteps(defaultRunner, &cfg)[4].revert(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"remove team-root ABCD"}; !slices.Equal(*calls, want) {
		t.Errorf("calls = %v, want %v", *calls, want)
	}
}
