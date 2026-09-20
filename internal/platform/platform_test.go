package platform

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fakeSudo puts a `sudo` on PATH that appends its arguments to a log file and
// exits with exitCode, and makes the process look unprivileged. It returns a
// func reading the recorded invocations.
func fakeSudo(t *testing.T, exitCode int) func() []string {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + log + "\nexit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "sudo"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	prependPATH(t, dir)
	unprivileged(t)

	return func() []string {
		b, err := os.ReadFile(log)
		if err != nil {
			return nil
		}
		return strings.Split(strings.TrimSpace(string(b)), "\n")
	}
}

// prependPATH puts dir first on PATH for the test, leaving the rest of the
// host's PATH reachable.
func prependPATH(t *testing.T, dir string) {
	t.Helper()
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// unprivileged makes isRoot report false, so a suite running as root (the Go
// image does) still exercises the sudo path.
func unprivileged(t *testing.T) {
	t.Helper()
	old := isRoot
	isRoot = func() bool { return false }
	t.Cleanup(func() { isRoot = old })
}

// captureStderr redirects os.Stderr for the test and returns a func reading
// what was written to it.
func captureStderr(t *testing.T) func() string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "stderr")
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = f
	t.Cleanup(func() { os.Stderr = old; f.Close() })

	return func() string {
		b, err := os.ReadFile(f.Name())
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
}

// TestSudoPrimeSaysNothingWhenSudoNeedsNoPassword asserts the passwordless host
// is settled non-interactively — probe, then a silent timestamp refresh — and
// that the administrator-privileges notice is not printed.
func TestSudoPrimeSaysNothingWhenSudoNeedsNoPassword(t *testing.T) {
	calls := fakeSudo(t, 0)
	stderr := captureStderr(t)

	SudoPrime("do the thing")

	if got := calls(); len(got) != 2 || got[0] != "-n true" || got[1] != "-n -v" {
		t.Fatalf("expected the probe then a non-interactive refresh, got %q", got)
	}
	if got := stderr(); got != "" {
		t.Fatalf("expected silence, got %q", got)
	}
}

// TestSudoPrimeIsBestEffortWhenPrimingFails asserts a sudo that refuses the
// probe still gets the interactive prime, that its failure is not fatal, and
// that the caller is told the command carries on.
func TestSudoPrimeIsBestEffortWhenPrimingFails(t *testing.T) {
	calls := fakeSudo(t, 1)
	stderr := captureStderr(t)

	SudoPrime("do the thing")

	if got := calls(); len(got) != 2 || got[0] != "-n true" || got[1] != "-v" {
		t.Fatalf("expected probe then prime, got %q", got)
	}
	got := stderr()
	if !strings.Contains(got, "administrator privileges to do the thing") {
		t.Fatalf("expected the purpose to be named, got %q", got)
	}
	if !strings.Contains(got, "continuing") {
		t.Fatalf("expected the failure to be reported as non-fatal, got %q", got)
	}
}

// TestSudoPrimeDoesNothingAsRoot asserts root never shells out to sudo.
func TestSudoPrimeDoesNothingAsRoot(t *testing.T) {
	calls := fakeSudo(t, 0)
	isRoot = func() bool { return true }
	stderr := captureStderr(t)

	SudoPrime("do the thing")

	if got := calls(); got != nil {
		t.Fatalf("expected no sudo calls as root, got %q", got)
	}
	if got := stderr(); got != "" {
		t.Fatalf("expected silence, got %q", got)
	}
}

// TestSudoPrimeDoesNothingWithoutSudo asserts a host with no sudo at all is
// left to the privileged commands rather than warned about here.
func TestSudoPrimeDoesNothingWithoutSudo(t *testing.T) {
	unprivileged(t)
	t.Setenv("PATH", t.TempDir())
	stderr := captureStderr(t)

	SudoPrime("do the thing")

	if got := stderr(); got != "" {
		t.Fatalf("expected silence, got %q", got)
	}
}
