package checks

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// unverifiable are the troubleshooting sections no Check can answer, each with
// the reason. It is exhaustive on purpose: together with the test below it
// makes every new section a decision — write a check, or say here why the
// environment cannot be asked — instead of a documented failure quietly having
// no check.
var unverifiable = map[string]string{
	"macos-udp-forwarding":                                   "a property of the Docker VM, visible only as the DNS checks failing",
	"macos-gatekeeper-blocks-the-binary":                     "about the binary before it runs, so nothing of proximo is running to ask",
	"traefik-logs-failed-to-find-any-pem-data":               "a fixed race in an older version; the log line is the only trace",
	"where-to-read-watcher-warnings":                         "a pointer to the logs, not a statement about the host",
	"an-error-i-typed-in-the-browser-console-never-shows-up": "browser behaviour proximo cannot observe",
	"proximo-errors-shows-nothing-for-an-inspected-route":    "a checklist about one route's traffic, answered by `proximo errors` itself",
	"an-inspected-route-404s-on-part-of-my-app":              "a property of the project's own paths, which proximo does not know",
	"502503-right-after-a-container-restarts":                "cured by adding a healthcheck; until one exists there is nothing to read",
	"the-stack-image-cannot-be-pulled":                       "carried by the converge that fails, which prints the Remedy itself",
}

// Every check names a section of docs/troubleshooting.md, and this test asserts
// the anchor exists. Making the link a test constraint is what stops a check
// from having no explanation.
func TestEveryCheckDocAnchorExists(t *testing.T) {
	anchors := troubleshootingSections(t)
	for _, c := range All(healthyEnv()) {
		if !anchors[c.Doc] {
			t.Errorf("check %q names docs/troubleshooting.md#%s, which has no section", c.ID, c.Doc)
		}
	}
	// A failure may name a section of its own instead of its check's.
	for _, doc := range []string{"a-host-collision-is-reported"} {
		if !anchors[doc] {
			t.Errorf("a failure names docs/troubleshooting.md#%s, which has no section", doc)
		}
	}
}

// And the other direction: a documented failure with no check is the drift that
// made this ADR necessary, so a section must either be checked or be listed
// above as one the environment cannot answer.
func TestEverySectionIsCheckedOrDeclaredUnverifiable(t *testing.T) {
	checked := map[string]bool{"a-host-collision-is-reported": true}
	for _, c := range All(healthyEnv()) {
		checked[c.Doc] = true
	}

	sections := troubleshootingSections(t)
	for anchor := range sections {
		if !checked[anchor] && unverifiable[anchor] == "" {
			t.Errorf("docs/troubleshooting.md#%s has no check: write one, or record in `unverifiable` why the environment cannot be asked", anchor)
		}
	}
	for anchor := range unverifiable {
		switch {
		case !sections[anchor]:
			t.Errorf("`unverifiable` names #%s, which is no longer a section", anchor)
		case checked[anchor]:
			t.Errorf("#%s is checked and also listed as unverifiable", anchor)
		}
	}
}

func troubleshootingSections(t *testing.T) map[string]bool {
	t.Helper()
	data, err := os.ReadFile("../../docs/troubleshooting.md")
	if err != nil {
		t.Fatalf("read troubleshooting: %v", err)
	}
	anchors := map[string]bool{}
	for _, line := range strings.Split(string(data), "\n") {
		if heading, ok := strings.CutPrefix(line, "## "); ok {
			anchors[slug(heading)] = true
		}
	}
	return anchors
}

var nonSlug = regexp.MustCompile(`[^a-z0-9 -]`)

// slug reproduces GitHub's heading anchors: lowercase, punctuation dropped,
// spaces hyphenated.
func slug(heading string) string {
	s := strings.ToLower(strings.TrimSpace(heading))
	s = nonSlug.ReplaceAllString(s, "")
	return strings.ReplaceAll(s, " ", "-")
}
