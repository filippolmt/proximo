package inspect

import (
	"strings"
	"testing"
)

// TestAgent pins the contract between the injected script and the Go that reads
// what it sends. The two halves live in different languages and nothing compiles
// them together, so the field names and the endpoint are checked here.
func TestAgent(t *testing.T) {
	agent, err := Agent()
	if err != nil {
		t.Fatalf("Agent(): %v", err)
	}
	js := string(agent)

	for _, want := range []string{
		ReservedPath + "ingest", // where reports go, same-origin
		"data-proximo-exchange", // how it learns the Exchange id
		`addEventListener("error"`,
		`addEventListener("unhandledrejection"`,
		`addEventListener("securitypolicyviolation"`,
	} {
		if !strings.Contains(js, want) {
			t.Errorf("agent is missing %q", want)
		}
	}

	// Every field report.go reads must be one the agent actually sets — as an
	// object literal key or by assignment, since the report is built both ways.
	for _, field := range []string{"type", "level", "message", "file", "line", "col", "stack", "dom", "breadcrumbs"} {
		if !strings.Contains(js, field+":") && !strings.Contains(js, "."+field+" =") {
			t.Errorf("agent never sets the %q field that report.go decodes", field)
		}
	}

	// The agent must not report on itself: its own requests would otherwise
	// appear as breadcrumbs of the page it is instrumenting.
	if !strings.Contains(js, `indexOf("`+ReservedPath+`")`) {
		t.Error("the agent does not exclude its own requests from breadcrumbs")
	}
}
