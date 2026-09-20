// Package platform provides OS / package-manager detection and the small set
// of privileged and unprivileged host operations the CLI relies on.
package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// OS identifies a supported operating system.
type OS string

const (
	// MacOS is Apple macOS (Darwin).
	MacOS OS = "macos"
	// Linux is Ubuntu/Debian Linux (the only supported family in v1).
	Linux OS = "linux"
)

// PackageManager identifies a supported package manager.
type PackageManager string

const (
	// Brew is Homebrew (macOS).
	Brew PackageManager = "brew"
	// Apt is APT (Ubuntu/Debian).
	Apt PackageManager = "apt"
)

// Dispatch runs the handler matching the current OS, erroring on unsupported
// platforms. It is the single place per-OS behavior branches.
func Dispatch(mac, linux func() error) error {
	osType, err := Current()
	if err != nil {
		return err
	}
	switch osType {
	case MacOS:
		return mac()
	case Linux:
		return linux()
	}
	return nil
}

// Pick returns the value matching the current OS, erroring on unsupported
// platforms. It is Dispatch for the answers that are a value rather than an
// error, so per-OS knowledge keeps to the one branch point instead of growing a
// switch at every call site.
func Pick[T any](mac, linux T) (T, error) {
	osType, err := Current()
	if err != nil {
		var zero T
		return zero, err
	}
	if osType == MacOS {
		return mac, nil
	}
	return linux, nil
}

// Current returns the running OS, erroring on unsupported platforms.
func Current() (OS, error) {
	switch runtime.GOOS {
	case "darwin":
		return MacOS, nil
	case "linux":
		return Linux, nil
	default:
		return "", fmt.Errorf("unsupported platform %q: only macOS and Ubuntu/Debian Linux are supported", runtime.GOOS)
	}
}

// DetectPackageManager returns the available package manager for the host.
func DetectPackageManager() (PackageManager, error) {
	if Has("brew") {
		return Brew, nil
	}
	if Has("apt-get") {
		return Apt, nil
	}
	return "", errors.New("no supported package manager found: need Homebrew (macOS) or apt (Ubuntu/Debian)")
}

// Has reports whether an executable is available on PATH.
func Has(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// Runner is the privileged/host exec surface the install/uninstall host steps
// mutate through. ExecRunner is the production adapter — each method delegates
// to the matching package function unchanged — so production behavior is
// identical; tests pass a fake that records calls to assert step ordering and
// rollback without touching the host.
type Runner interface {
	Run(name string, args ...string) error
	Sudo(args ...string) error
	WriteFilePrivileged(path string, content []byte, mode os.FileMode) error
	RemoveFilePrivileged(path string) error
}

// ExecRunner is the production Runner. It carries no state and forwards every
// call to the package-level functions, so routing host steps through a Runner
// changes no observable behavior.
type ExecRunner struct{}

func (ExecRunner) Run(name string, args ...string) error { return Run(name, args...) }

func (ExecRunner) Sudo(args ...string) error { return Sudo(args...) }

func (ExecRunner) WriteFilePrivileged(path string, content []byte, mode os.FileMode) error {
	return WriteFilePrivileged(path, content, mode)
}

func (ExecRunner) RemoveFilePrivileged(path string) error { return RemoveFilePrivileged(path) }

// Run executes a command, inheriting the parent's stdio.
func Run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Output runs a command and returns its standard output as a string.
func Output(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	return string(out), err
}

// OutputContext runs a command under a context and returns its standard output.
// Every host reading that shells out goes through here: a check that runs out of
// time must fail rather than hang.
func OutputContext(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).Output()
	return string(out), err
}

// isRoot is a var so tests can run the unprivileged path as root.
var isRoot = func() bool { return os.Geteuid() == 0 }

// Sudo runs a command with elevated privileges, prompting once if needed.
// When already running as root it executes the command directly.
func Sudo(args ...string) error {
	if len(args) == 0 {
		return errors.New("sudo: no command given")
	}
	if isRoot() {
		return Run(args[0], args[1:]...)
	}
	return Run("sudo", args...)
}

// sudoProbeTimeout bounds the non-interactive sudo calls below. Under `-n` sudo
// never waits for a human, but its auth backend (LDAP, sssd) can hang, and
// priming — an optimization — must not be what blocks a command forever.
const sudoProbeTimeout = 5 * time.Second

// SudoPrime primes the sudo credential cache so subsequent privileged calls do
// not each prompt for a password. It is best-effort and never fatal: `sudo -v`
// demands a password unless *every* sudoers entry matching the user is NOPASSWD,
// so where sudo itself asks nothing it would still prompt — and without a TTY
// fail — an install every later `sudo <cmd>` would have completed. The
// non-interactive probe settles that without asking, and only the privileged
// calls themselves can say whether the host is usable.
func SudoPrime(purpose string) {
	if isRoot() || !Has("sudo") {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), sudoProbeTimeout)
	defer cancel()
	if _, err := OutputContext(ctx, "sudo", "-n", "true"); err == nil {
		// Nothing to ask for. Extend the credential timestamp if there is one,
		// so a step further down is not left to prompt when it expires; where
		// no password is ever needed this fails, harmlessly and silently.
		_, _ = OutputContext(ctx, "sudo", "-n", "-v")
		return
	}
	fmt.Fprintf(os.Stderr, "proximo needs administrator privileges to %s.\n", purpose)
	if err := Run("sudo", "-v"); err != nil {
		fmt.Fprintln(os.Stderr, "sudo could not be primed; continuing — the privileged commands will ask for themselves.")
	}
}

// WriteFilePrivileged writes content to a root-owned path, creating parent
// directories as needed.
func WriteFilePrivileged(path string, content []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp("", "proximo-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := Sudo("mkdir", "-p", filepath.Dir(path)); err != nil {
		return err
	}
	return Sudo("install", "-m", fmt.Sprintf("%o", mode.Perm()), tmp.Name(), path)
}

// RemoveFilePrivileged removes a root-owned file, ignoring a missing file.
func RemoveFilePrivileged(path string) error {
	return Sudo("rm", "-f", path)
}

// InstallPackage installs a package using the detected package manager.
func InstallPackage(pkg string) error {
	pm, err := DetectPackageManager()
	if err != nil {
		return err
	}
	switch pm {
	case Brew:
		return Run("brew", "install", pkg)
	case Apt:
		if err := Sudo("apt-get", "update"); err != nil {
			return err
		}
		return Sudo("apt-get", "install", "-y", pkg)
	}
	return fmt.Errorf("cannot install %q: unsupported package manager", pkg)
}

// IsActiveService reports whether a systemd unit is active (Linux only).
func IsActiveService(unit string) bool {
	out, _ := Output("systemctl", "is-active", unit)
	return strings.TrimSpace(out) == "active"
}
