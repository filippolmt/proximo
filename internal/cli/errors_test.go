package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/filippolmt/proximo/internal/inspect"
)

// TestWriteExchange pins the output shape. It is a contract, not a cosmetic
// choice: an agent reads these blocks as often as a person does, so the field
// order and the file:line form must not drift.
func TestWriteExchange(t *testing.T) {
	at := time.Date(2026, 8, 30, 14, 5, 9, 0, time.Local)
	e := inspect.Exchange{
		ID: "9f3a21ab", At: at, Host: "web.test", Method: "GET", Path: "/checkout",
		Status: 200, Duration: 184 * time.Millisecond, HasSnapshot: true,
		Reports: []inspect.Report{{
			Message: "TypeError: Cannot read properties of undefined (reading 'total')",
			Frames: []inspect.Frame{
				{File: "src/checkout/Summary.tsx", Line: 47, Col: 18, Func: "renderSummary"},
				{File: "src/checkout/Page.tsx", Line: 12, Func: "onMount"},
			},
			Breadcrumbs: []inspect.Breadcrumb{
				{Level: "error", Category: "fetch", Message: "GET /api/cart 500"},
				{Level: "info", Category: "console", Message: "HMR update"},
			},
		}},
	}

	var b strings.Builder
	writeExchange(&b, e, warnAndAbove)
	out := b.String()

	for _, want := range []string{
		"14:05:09  9f3a21ab  GET /checkout  →  200  184ms",
		"✗ TypeError: Cannot read properties of undefined (reading 'total')",
		"at renderSummary (src/checkout/Summary.tsx:47:18)",
		"at onMount (src/checkout/Page.tsx:12)",
		"GET /api/cart 500",
		"proximo errors dom 9f3a21ab",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\n---\n%s", want, out)
		}
	}
	// Dev-server chatter is hidden by default and kept by --all: nothing is lost
	// at capture time, only at display time.
	if strings.Contains(out, "HMR update") {
		t.Errorf("info breadcrumbs must be hidden without --all\n---\n%s", out)
	}
	b.Reset()
	writeExchange(&b, e, everything)
	if !strings.Contains(b.String(), "HMR update") {
		t.Error("--all must show every breadcrumb")
	}
}

// TestWriteExchangeBackendFailure: an Exchange with no Client report and a
// failing status says so, rather than leaving a reader to wonder whether the
// agent simply never fired.
func TestWriteExchangeBackendFailure(t *testing.T) {
	var b strings.Builder
	writeExchange(&b, inspect.Exchange{ID: "a1", At: time.Now(), Method: "GET", Path: "/api/cart", Status: 500, Duration: 1200 * time.Millisecond}, warnAndAbove)
	out := b.String()
	if !strings.Contains(out, "1.2s") {
		t.Errorf("sub-second and second scales must both render: %q", out)
	}
	if !strings.Contains(out, "the failure is the backend's") {
		t.Errorf("missing the no-client-report note\n---\n%s", out)
	}
}

// TestWriteExchangeWarnings: the CSP relaxation must be visible wherever the
// developer looks at the route, never silent.
func TestWriteExchangeWarnings(t *testing.T) {
	var b strings.Builder
	writeExchange(&b, inspect.Exchange{
		ID: "a1", At: time.Now(), Method: "GET", Path: "/", Status: 200,
		Warnings: []string{"relaxed Content-Security-Policy on this route so the proximo agent could load"},
	}, warnAndAbove)
	if !strings.Contains(b.String(), warnPrefix+"relaxed Content-Security-Policy") {
		t.Errorf("warning not shown\n---\n%s", b.String())
	}
}
