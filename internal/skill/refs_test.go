package skill

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// update rewrites the generated blocks instead of asserting them. It is what
// `make skill-refs` runs; without it this test is the CI check that the Skill's
// contracts have not drifted from docs/.
var update = flag.Bool("update", false, "rewrite the generated blocks in skills/proximo")

const repoRoot = "../.."

func TestGeneratedBlocksMatchDocs(t *testing.T) {
	files, err := Refs(repoRoot)
	if err != nil {
		t.Fatalf("render references: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no generated block found in skills/proximo: the source markers are gone")
	}

	for path, want := range files {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if bytes.Equal(got, want) {
			continue
		}
		if *update {
			if err := os.WriteFile(path, want, 0o644); err != nil {
				t.Fatalf("write %s: %v", path, err)
			}
			t.Logf("regenerated %s", path)
			continue
		}
		t.Errorf("%s is out of date with docs/; run `make skill-refs`", path)
	}
}

// The Skill is installed where no docs/ sits beside it, so a relative link out
// of it resolves to nothing. Links inside the Skill are the exception: those
// files travel together.
func TestSkillLinksOutAreAbsolute(t *testing.T) {
	root := filepath.Join(repoRoot, "skills", Name)
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range linkPattern.FindAllStringSubmatch(string(data), -1) {
			target := m[2]
			if strings.Contains(target, "://") || withinSkill(root, path, target) {
				continue
			}
			t.Errorf("%s links to %q, which does not resolve once installed", path, target)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk skill: %v", err)
	}
}

// withinSkill reports whether a relative target stays inside the Skill tree.
func withinSkill(root, from, target string) bool {
	file, _, _ := strings.Cut(target, "#")
	if file == "" {
		return true
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(from), file))
	rel, err := filepath.Rel(root, resolved)
	if err != nil || strings.HasPrefix(rel, "..") {
		return false
	}
	_, err = os.Stat(resolved)
	return err == nil
}

// The site serves docs/ as its root, so a link one directory down keeps that
// directory. Flattening it would ship a 404 into every installed copy, and the
// generator would report nothing, because the target still ends in `.md`.
func TestAbsolutizeKeepsTheDocsPath(t *testing.T) {
	got, err := absolutize("[ADR 0003](adr/0003-every-route-answers-on-a-qualified-host.md) "+
		"[labels](routing.md#the-proximo-labels) "+
		"[here](#a-host-collision-is-reported) "+
		"[site](https://filippolmt.github.io/proximo/)\n", "docs/troubleshooting.md")
	if err != nil {
		t.Fatalf("absolutize: %v", err)
	}
	for _, want := range []string{
		Site + "adr/0003-every-route-answers-on-a-qualified-host.html",
		Site + "routing.html#the-proximo-labels",
		Site + "troubleshooting.html#a-host-collision-is-reported",
		Site + ")", // the absolute link is left exactly as it was
	} {
		if !strings.Contains(got, want) {
			t.Errorf("absolutize dropped %q:\n%s", want, got)
		}
	}
}

// A link out of docs/ has no page on the site, and must fail loudly rather than
// be published as a broken URL.
func TestAbsolutizeRejectsLinksOutsideDocs(t *testing.T) {
	if _, err := absolutize("[glossary](../CONTEXT.md)\n", "docs/routing.md"); err == nil {
		t.Error("a link outside docs/ was accepted")
	}
}
