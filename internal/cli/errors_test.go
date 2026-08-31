package cli

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/filippolmt/proximo/internal/inspect"
	"github.com/filippolmt/proximo/internal/transcript"
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
	writeExchange(&b, e, transcript.Transcript{}, warnAndAbove)
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
	writeExchange(&b, e, transcript.Transcript{}, everything)
	if !strings.Contains(b.String(), "HMR update") {
		t.Error("--all must show every breadcrumb")
	}
}

// TestWriteExchangeBackendFailure: an Exchange with no Client report and a
// failing status says so, rather than leaving a reader to wonder whether the
// agent simply never fired.
func TestWriteExchangeBackendFailure(t *testing.T) {
	var b strings.Builder
	writeExchange(&b, inspect.Exchange{ID: "a1", At: time.Now(), Method: "GET", Path: "/api/cart", Status: 500, Duration: 1200 * time.Millisecond}, transcript.Transcript{}, warnAndAbove)
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
	}, transcript.Transcript{}, warnAndAbove)
	if !strings.Contains(b.String(), warnPrefix+"relaxed Content-Security-Policy") {
		t.Errorf("warning not shown\n---\n%s", b.String())
	}
}

// TestParseSince: an agent knows when it saved the file it is asking about;
// proximo keeps no cursor and no state, so the instant has to be sayable.
// A duration stays the convenient form for a person.
func TestParseSince(t *testing.T) {
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)

	got, err := parseSince("15m", now)
	if err != nil {
		t.Fatalf("parseSince(15m): %v", err)
	}
	if want := now.Add(-15 * time.Minute); !got.Equal(want) {
		t.Errorf("parseSince(15m) = %v, want %v", got, want)
	}

	instant := "2026-08-31T10:30:00Z"
	got, err = parseSince(instant, now)
	if err != nil {
		t.Fatalf("parseSince(%s): %v", instant, err)
	}
	if want, _ := time.Parse(time.RFC3339, instant); !got.Equal(want) {
		t.Errorf("parseSince(%s) = %v, want %v", instant, got, want)
	}

	// Anything else must name both forms rather than silently defaulting: a
	// window quietly wider than asked for is a wrong answer that looks right.
	_, err = parseSince("yesterday", now)
	if err == nil {
		t.Fatal("parseSince accepted a value it cannot honour")
	}
	for _, want := range []string{"15m", "RFC 3339"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name the %s form", err, want)
		}
	}
}

// A Transcript is quoted only where there is something to say. A clean Exchange
// shows none: burying the one broken page under every container's healthy
// chatter is the failure the default listing already refuses.
func TestTranscriptIsQuotedOnlyWhereThereIsSomethingToSay(t *testing.T) {
	tr := transcript.Transcript{Container: "web-1", Head: []string{"listening on :8080"}}
	clean := inspect.Exchange{ID: "a1", At: time.Now(), Method: "GET", Path: "/", Status: 200}

	var b strings.Builder
	writeExchange(&b, clean, tr, warnAndAbove)
	if strings.Contains(b.String(), "listening on :8080") {
		t.Errorf("a clean Exchange quoted a Transcript\n---\n%s", b.String())
	}

	b.Reset()
	failing := clean
	failing.Status = 500
	writeExchange(&b, failing, tr, warnAndAbove)
	if !strings.Contains(b.String(), "listening on :8080") {
		t.Errorf("a failing Exchange did not quote its Transcript\n---\n%s", b.String())
	}
}

// The elision is declared, both ends survive, and the way to read the rest is on
// the page: a truncation nobody is told about is the one after which a reader
// stops looking.
func TestTranscriptDeclaresItsElisionAndItsReplicas(t *testing.T) {
	var b strings.Builder
	writeExchange(&b, inspect.Exchange{ID: "a1", At: time.Now(), Method: "GET", Path: "/", Status: 500},
		transcript.Transcript{
			Container: "web-1", Replicas: 3,
			Head: []string{"panic: nil map"}, Tail: []string{"exit status 2"}, Dropped: 412,
		}, warnAndAbove)
	out := b.String()

	for _, want := range []string{
		"web-1",
		"3 replicas",
		"panic: nil map",
		"412",
		"exit status 2",
		"proximo errors transcript a1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\n---\n%s", want, out)
		}
	}
}

// Never a silence without a named cause, and never a line attributed to the
// wrong request.
func TestTranscriptReportsSilenceAndOverlap(t *testing.T) {
	failing := inspect.Exchange{ID: "a1", At: time.Now(), Method: "GET", Path: "/", Status: 500}

	var b strings.Builder
	writeExchange(&b, failing, transcript.Transcript{
		Container: "web-1", Silence: "web-1 wrote nothing while this request was live",
	}, warnAndAbove)
	if !strings.Contains(b.String(), "wrote nothing while this request was live") {
		t.Errorf("the silence was not named\n---\n%s", b.String())
	}

	b.Reset()
	writeExchange(&b, failing, transcript.Transcript{
		Container: "web-1", Head: []string{"something happened"}, Overlap: 2,
	}, warnAndAbove)
	out := b.String()
	if !strings.Contains(out, "2") || !strings.Contains(strings.ToLower(out), "overlap") {
		t.Errorf("overlapping Exchanges were not reported\n---\n%s", out)
	}
}

// A Transcript is raw application output. It is said once per listing, in the
// CLI, and never softened into a claim that anything was removed.
func TestListingStatesTheCredentialRiskOnceAndOnlyWhenQuoting(t *testing.T) {
	failing := inspect.Exchange{ID: "a1", At: time.Now(), Method: "GET", Path: "/", Status: 500}
	quoted := map[string]transcript.Transcript{"a1": {Container: "web-1", Head: []string{"token=abc"}}}

	var b strings.Builder
	writeListing(&b, []inspect.Exchange{failing, failing}, quoted, warnAndAbove)
	out := b.String()
	if n := strings.Count(out, "may carry credentials"); n != 1 {
		t.Errorf("the credential notice appears %d times, want exactly 1\n---\n%s", n, out)
	}
	if strings.Contains(out, "redact") && !strings.Contains(out, "no redaction") {
		t.Errorf("the notice must not imply anything was removed\n---\n%s", out)
	}

	b.Reset()
	writeListing(&b, []inspect.Exchange{{ID: "b1", At: time.Now(), Method: "GET", Path: "/", Status: 200}}, nil, warnAndAbove)
	if strings.Contains(b.String(), "may carry credentials") {
		t.Errorf("nothing was quoted, so there is nothing to warn about\n---\n%s", b.String())
	}
}

// TestSelect: the Store and the CLI narrow by one shared rule, because the same
// flags must not mean two things.
func TestSelect(t *testing.T) {
	now := time.Now()
	all := []inspect.Exchange{
		{ID: "new-broken", At: now.Add(-time.Minute), Host: "web.test", Status: 500},
		{ID: "new-clean", At: now.Add(-2 * time.Minute), Host: "web.test", Status: 200},
		{ID: "other-host", At: now.Add(-3 * time.Minute), Host: "api.test", Status: 500},
		{ID: "too-old", At: now.Add(-time.Hour), Host: "web.test", Status: 500},
	}
	cutoff := now.Add(-15 * time.Minute)

	// A page served an hour ago that threw a moment ago is fresher news than a
	// request served since. The hop's Store orders and windows by that rule, and
	// re-filtering the merged listing by the request instant alone would drop the
	// one Client report this tool exists for.
	late := inspect.Exchange{
		ID: "late-report", At: now.Add(-time.Hour), Host: "web.test", Status: 200,
		Reports: []inspect.Report{{At: now, Message: "boom"}},
	}
	if got := inspect.Select(append(all, late), "", cutoff, 0, true); !slices.Contains(ids(got), "late-report") {
		t.Errorf("an Exchange that threw inside the window was dropped for having been served before it: %v", ids(got))
	}
	if got := inspect.Select(append(all, late), "", cutoff, 1, true); got[0].ID != "late-report" {
		t.Errorf("ordering must follow the most recent activity, got %v", ids(got))
	}

	got := inspect.Select(all, "web.test", cutoff, 0, true)
	if len(got) != 1 || got[0].ID != "new-broken" {
		t.Fatalf("host + window + only-problems selected %+v", ids(got))
	}

	got = inspect.Select(all, "", cutoff, 0, false)
	if len(got) != 3 {
		t.Errorf("selected %v, want the three inside the window", ids(got))
	}

	got = inspect.Select(all, "", cutoff, 2, false)
	if len(got) != 2 || got[0].ID != "new-broken" {
		t.Errorf("--limit must keep the most recent, got %v", ids(got))
	}
}

func ids(es []inspect.Exchange) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.ID
	}
	return out
}

// An empty listing for a named host has two opposite causes and must not be
// allowed to look like one: nothing called it, or the name does not reach
// proximo at all.
func TestNothingFoundForAHostNamesBothCauses(t *testing.T) {
	var b strings.Builder
	writeNothingFound(&b, false, "web.test")
	out := b.String()
	for _, want := range []string{"web.test", "proximo doctor"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\n---\n%s", want, out)
		}
	}
	if !strings.Contains(out, "resolve") {
		t.Errorf("the message does not offer the second cause\n---\n%s", out)
	}
}
