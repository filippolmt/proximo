package inspect

import (
	"strings"
	"testing"
)

// TestAgent pins the two halves of the injected script: the vendored SDK, which
// is committed rather than fetched, and proximo's own snippet on top of it. A
// checkout missing the bundle fails here rather than serving a script that
// silently does nothing.
func TestAgent(t *testing.T) {
	agent, err := Agent()
	if err != nil {
		t.Fatalf("Agent(): %v", err)
	}
	for _, want := range []string{
		"@sentry/browser",       // provenance banner
		"var Sentry=",           // the IIFE's global
		"Sentry.init({",         // our snippet
		"tunnel:",               // where reports go
		ReservedPath + "ingest", // ...which must be the reserved path
		"data-proximo-exchange", // how it learns the Exchange id
		snapshotFilename,        // the DOM attachment
	} {
		if !strings.Contains(string(agent), want) {
			t.Errorf("assembled agent missing %q", want)
		}
	}
	// The snippet must come after the SDK, or `Sentry` is undefined when it runs.
	if strings.Index(string(agent), "Sentry.init({") < strings.Index(string(agent), "var Sentry=") {
		t.Error("the snippet must be concatenated after the SDK")
	}
}
