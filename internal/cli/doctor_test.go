package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/filippolmt/proximo/internal/checks"
)

func render(o checks.Outcome) string {
	var buf bytes.Buffer
	writeOutcome(&buf, o)
	return buf.String()
}

func TestPassFitsOnOneLine(t *testing.T) {
	got := render(checks.Outcome{
		Check:  checks.Check{Name: "Nothing but proximo holds :443/tcp", Doc: "port-443-or-80-already-in-use"},
		Result: checks.Passed("held by the proximo stack (proximo-traefik-1)"),
	})
	want := "✔ Nothing but proximo holds :443/tcp — held by the proximo stack (proximo-traefik-1)\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPassWithoutDetailIsJustTheStatement(t *testing.T) {
	got := render(checks.Outcome{
		Check:  checks.Check{Name: "The Docker daemon is reachable"},
		Result: checks.Passed(""),
	})
	if got != "✔ The Docker daemon is reachable\n" {
		t.Errorf("got %q", got)
	}
}

// A failure always shows its Remedy and where the failure is explained — the
// whole point of the command, and what tells `doctor` apart from `status`.
func TestFailureCarriesRemedyAndDocLink(t *testing.T) {
	got := render(checks.Outcome{
		Check:  checks.Check{Name: "The host resolver uses the proximo DNS server", Doc: "vpn-or-corporate-dns-overrides-the-resolver"},
		Result: checks.Failed("resolvectl status", "proximo-doctor.test resolves to \"\", not 127.0.0.1"),
	})
	for _, want := range []string{
		"✘ The host resolver uses the proximo DNS server\n",
		"    proximo-doctor.test resolves to \"\", not 127.0.0.1\n",
		"    Remedy: resolvectl status\n",
		"    See:    docs/troubleshooting.md#vpn-or-corporate-dns-overrides-the-resolver\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q is missing %q", got, want)
		}
	}
}

func TestMultiLineDetailStaysIndented(t *testing.T) {
	got := render(checks.Outcome{
		Check:  checks.Check{Name: "Every routed container is served", Doc: "container-not-routed"},
		Result: checks.Failed("docker inspect a b", "a: taken\nb: no port"),
	})
	if !strings.Contains(got, "    a: taken\n    b: no port\n") {
		t.Errorf("detail lines are not indented: %q", got)
	}
}

func TestSkipNamesWhatItWaitedOn(t *testing.T) {
	got := render(checks.Outcome{
		Check:  checks.Check{Name: "The local CA is in the system trust store", Doc: "certificate-warnings-in-firefox-or-chrome"},
		Result: checks.Skipped("waiting on: proximo is installed on this host"),
	})
	want := "– The local CA is in the system trust store — waiting on: proximo is installed on this host\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if strings.Contains(got, "Remedy") {
		t.Error("a skip must not carry a remedy")
	}
}

// The report is one whole thing: it shows what passed too, because a pass says
// where not to look.
func TestReportShowsPassesAndFailures(t *testing.T) {
	var buf bytes.Buffer
	writeReport(&buf, checks.Report{Outcomes: []checks.Outcome{
		{Check: checks.Check{Name: "The Docker daemon is reachable"}, Result: checks.Passed("")},
		{Check: checks.Check{Name: "The proximo stack is running", Doc: "degraded-stack"},
			Result: checks.Failed("proximo up", "no stack container is running")},
	}})
	out := buf.String()
	if !strings.Contains(out, "✔ The Docker daemon is reachable") || !strings.Contains(out, "✘ The proximo stack is running") {
		t.Errorf("report dropped an outcome: %q", out)
	}
}

func TestCountFailedReadsAsASentence(t *testing.T) {
	if got := countFailed(1); got != "1 check failed" {
		t.Errorf("got %q", got)
	}
	if got := countFailed(3); got != "3 checks failed" {
		t.Errorf("got %q", got)
	}
}
