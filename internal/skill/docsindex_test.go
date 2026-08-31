package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// docs/README.md calls itself "the canonical section-level map" and tells
// editors to add a row whenever they add a `##` section to a guide. That
// instruction was already false of eight sections when this test was written: a
// map nobody enforces drifts, and a drifted map is worse than none, because a
// reader who finds four of five sections listed concludes the fifth does not
// exist.
//
// This test lives in the skill package because that package already reads
// `docs/` and already knows how a heading becomes a GitHub anchor — the Skill's
// generated blocks are addressed the same way (refs.go).
func TestEveryDocsSectionIsInTheIndex(t *testing.T) {
	const index = "README.md"
	root := filepath.Join("..", "..", "docs")

	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read docs/: %v", err)
	}
	indexed, err := os.ReadFile(filepath.Join(root, index))
	if err != nil {
		t.Fatalf("read docs/%s: %v", index, err)
	}

	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".md" || e.Name() == index {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, e.Name()))
		if err != nil {
			t.Fatalf("read docs/%s: %v", e.Name(), err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			heading, ok := strings.CutPrefix(line, "## ")
			if !ok {
				continue
			}
			link := e.Name() + "#" + slug(heading)
			if !strings.Contains(string(indexed), link) {
				t.Errorf("docs/%s has section %q, which docs/%s does not link (%s) — add the row, the way that file says to",
					e.Name(), heading, index, link)
			}
		}
	}
}

// And the other direction: a link to a section that no longer exists. lychee
// catches a broken anchor in CI, but only when it runs; this keeps the two
// directions of the same contract in one place.
func TestTheIndexLinksNoSectionThatIsGone(t *testing.T) {
	root := filepath.Join("..", "..", "docs")
	indexed, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatalf("read docs/README.md: %v", err)
	}
	for _, m := range linkPattern.FindAllStringSubmatch(string(indexed), -1) {
		target := m[2]
		file, anchor, ok := strings.Cut(target, "#")
		// Only same-directory guide anchors: the index also links ../CONTEXT.md,
		// the ADRs and whole files, and those are lychee's business.
		if !ok || anchor == "" || strings.Contains(file, "/") || !strings.HasSuffix(file, ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Errorf("docs/README.md links %s, which does not exist", target)
			continue
		}
		// Any heading level: the index links a few `###` subsections deliberately
		// (`proximo errors transcript`), and what has to exist is the anchor.
		found := false
		for _, line := range strings.Split(string(data), "\n") {
			heading := strings.TrimLeft(line, "#")
			if len(heading) == len(line) || !strings.HasPrefix(heading, " ") {
				continue
			}
			if slug(strings.TrimSpace(heading)) == anchor {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("docs/README.md links %s, which is no longer a heading of that guide", target)
		}
	}
}
