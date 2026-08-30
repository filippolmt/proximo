package inspect

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// The parser in envelope.go reads a wire format proximo does not own. The tests
// beside it use envelopes written by hand, which prove the parser handles the
// shapes we expect — not that those are the shapes @sentry/browser actually
// sends. This file closes that gap with an envelope captured from the vendored
// agent running in a real browser (`make capture-envelope`), and pins it to the
// version it came from so an SDK bump cannot quietly leave it behind.

var (
	bannerVersion  = regexp.MustCompile(`@sentry/browser ([0-9]+\.[0-9]+\.[0-9]+)`)
	bannerESBuild  = regexp.MustCompile(`esbuild ([0-9]+\.[0-9]+\.[0-9]+)`)
	makefileSentry = regexp.MustCompile(`(?m)^SENTRY_VERSION\s*\?=\s*(\S+)`)
	makefileESBuil = regexp.MustCompile(`(?m)^ESBUILD_VERSION\s*\?=\s*(\S+)`)
)

// bannerOf reads the provenance line `make vendor-agent` stamps on the bundle.
func bannerOf(t *testing.T) string {
	t.Helper()
	sdk, err := assets.ReadFile(sdkPath)
	if err != nil {
		t.Skip("vendored SDK missing; run `make vendor-agent`")
	}
	return string(sdk[:min(len(sdk), 200)])
}

// TestVendoredSDKMatchesMakefile is what makes Renovate safe here. Renovate can
// bump SENTRY_VERSION or ESBUILD_VERSION in the Makefile, but it cannot rebuild a
// committed binary artifact — so on its own such a PR would be green and ship
// nothing, the pin claiming one version while the bundle in the image stays
// another. This fails until someone runs `make vendor-agent && make
// capture-envelope`.
func TestVendoredSDKMatchesMakefile(t *testing.T) {
	makefile, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	banner := bannerOf(t)

	for _, c := range []struct {
		what            string
		pinned, stamped *regexp.Regexp
	}{
		{"@sentry/browser", makefileSentry, bannerVersion},
		{"esbuild", makefileESBuil, bannerESBuild},
	} {
		want := c.pinned.FindSubmatch(makefile)
		got := c.stamped.FindStringSubmatch(banner)
		if want == nil || got == nil {
			t.Fatalf("%s: cannot read the pin (%v) or the banner (%v)", c.what, want != nil, got != nil)
		}
		if string(want[1]) != got[1] {
			t.Errorf("%s: the Makefile pins %s but the vendored bundle was built from %s — run `make vendor-agent && make capture-envelope`",
				c.what, want[1], got[1])
		}
	}
}

// TestFixtureMatchesVendoredSDK is the guard: bumping SENTRY_VERSION without
// re-capturing the fixture fails here, so the checks below are never silently
// testing a format the shipped agent no longer produces.
func TestFixtureMatchesVendoredSDK(t *testing.T) {
	m := bannerVersion.FindStringSubmatch(bannerOf(t))
	if m == nil {
		t.Fatal("the vendored bundle has no provenance banner")
	}
	want, err := os.ReadFile("testdata/envelope.version")
	if err != nil {
		t.Fatalf("read fixture version: %v", err)
	}
	if got := m[1]; got != strings.TrimSpace(string(want)) {
		t.Fatalf("the fixture was captured from @sentry/browser %s but the vendored bundle is %s — run `make capture-envelope`",
			strings.TrimSpace(string(want)), got)
	}
}

// TestDecodeRealEnvelope runs the parser over bytes the SDK really produced. It
// covers the two couplings that no hand-written fixture can: the envelope wire
// format, and the DOM Snapshot arriving as an attachment — which proximo adds by
// mutating `hint.attachments` inside `beforeSend`, behaviour Sentry documents
// loosely enough that a minor release could take it away without an error.
func TestDecodeRealEnvelope(t *testing.T) {
	raw, err := os.ReadFile("testdata/envelope.bin")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	in, err := decode(raw)
	if err != nil {
		t.Fatalf("decode a real envelope: %v", err)
	}
	if !in.Found {
		t.Fatal("no event item found in a captured exception envelope")
	}
	if in.Report.Type == "" || in.Report.Message == "" {
		t.Errorf("exception not parsed: %+v", in.Report)
	}
	if len(in.Report.Frames) == 0 {
		t.Error("no stack frames parsed")
	}
	for _, f := range in.Report.Frames {
		if strings.Contains(f.File, ReservedPath) {
			t.Errorf("the agent's own frame survived: %+v", f)
		}
	}
	if in.Report.At.IsZero() {
		t.Error("timestamp not parsed")
	}
	if len(in.Report.Breadcrumbs) == 0 {
		t.Error("no breadcrumbs parsed — the capture logs a console warning before it throws")
	}
	if !strings.Contains(string(in.Snapshot), "<html") {
		t.Errorf("DOM Snapshot missing or not HTML (%d bytes) — check beforeSend still honours hint.attachments", len(in.Snapshot))
	}
}
