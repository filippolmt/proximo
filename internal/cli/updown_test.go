package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/filippolmt/proximo/internal/docker"
)

// TestReportImageAnnouncesWhatWillRun pins the property that makes the line
// worth printing: it names the ref the converge is about to use, resolved the
// same way the converge resolves it. Announcing the canonical ref while a
// PROXIMO_SRC checkout starts a locally built image would be the "runs one
// thing, declares another" defect in the output instead of in the labels.
func TestReportImageAnnouncesWhatWillRun(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("PROXIMO_SRC", "")

	// A stack already materialized at a published ref.
	prev := "ghcr.io/filippolmt/proximo:v0.0.1"
	if _, err := docker.Materialize("test", "", prev); err != nil {
		t.Fatalf("Materialize: %v", err)
	}

	t.Run("names the local image under PROXIMO_SRC", func(t *testing.T) {
		t.Setenv("PROXIMO_SRC", t.TempDir())
		var out bytes.Buffer
		reportImage(&out, docker.ConvergeOpts{})
		line := out.String()
		if !strings.Contains(line, docker.ConvergeOpts{}.EffectiveImage()) {
			t.Errorf("reportImage = %q, want the ref the converge will run", line)
		}
		if !strings.Contains(line, prev) {
			t.Errorf("reportImage = %q, want the ref it is replacing", line)
		}
	})

	t.Run("silent when nothing changes", func(t *testing.T) {
		var out bytes.Buffer
		reportImage(&out, docker.ConvergeOpts{Image: prev})
		if out.Len() != 0 {
			t.Errorf("reportImage = %q, want nothing when the ref is unchanged", out.String())
		}
	})
}
