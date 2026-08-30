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

	// Every field report.go reads must be one the agent actually sets. Matching a
	// bare `field:` is too weak — `type:` alone is satisfied by the Blob's
	// `type: "application/json"` — so each field is anchored to the line that
	// really produces it.
	for field, produces := range map[string]string{
		"type":        `type: (err && err.name)`,
		"level":       `level: "error"`,
		"message":     `message: (err && err.message)`,
		"file":        `file: ev.filename`,
		"line":        `line: ev.lineno`,
		"col":         `col: ev.colno`,
		"stack":       `stack: (err && err.stack)`,
		"dom":         `fields.dom = document.documentElement.outerHTML`,
		"breadcrumbs": `fields.breadcrumbs = breadcrumbs.slice()`,
	} {
		if !strings.Contains(js, produces) {
			t.Errorf("agent no longer produces the %q field the way report.go expects (looked for %q)", field, produces)
		}
	}

	// The agent must not report on itself: its own requests would otherwise
	// appear as breadcrumbs of the page it is instrumenting.
	if !strings.Contains(js, `indexOf("`+ReservedPath+`")`) {
		t.Error("the agent does not exclude its own requests from breadcrumbs")
	}
}
