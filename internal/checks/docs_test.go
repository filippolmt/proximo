package checks

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Every check names a section of docs/troubleshooting.md, and this test asserts
// the anchor exists. Making the link a test constraint is what stops a
// documented failure from having no check, and a check from having no
// explanation.
func TestEveryCheckDocAnchorExists(t *testing.T) {
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

	for _, c := range All(healthyEnv()) {
		if !anchors[c.Doc] {
			t.Errorf("check %q names docs/troubleshooting.md#%s, which has no section", c.ID, c.Doc)
		}
	}
}

var nonSlug = regexp.MustCompile(`[^a-z0-9 -]`)

// slug reproduces GitHub's heading anchors: lowercase, punctuation dropped,
// spaces hyphenated.
func slug(heading string) string {
	s := strings.ToLower(strings.TrimSpace(heading))
	s = nonSlug.ReplaceAllString(s, "")
	return strings.ReplaceAll(s, " ", "-")
}
