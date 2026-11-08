package docker

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Compose's label keys are spelled once, in watcher.go, next to stackProject.
// A literal elsewhere drifts from them silently — the Compose project name in
// particular, which the stack network's name and every project filter derive
// from — so production code must use the constants.
func TestComposeLabelsAreNotSpelledOut(t *testing.T) {
	const needle = `"com.docker.compose.`
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "vendor") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		s := bufio.NewScanner(f)
		for n := 1; s.Scan(); n++ {
			line := s.Text()
			if !strings.Contains(line, needle) {
				continue
			}
			// The one place the keys are declared.
			if filepath.Base(path) == "watcher.go" && strings.Contains(line, "Label = "+needle) {
				continue
			}
			t.Errorf("%s:%d spells out a Compose label; use composeProjectLabel or ComposeServiceLabel: %s", path, n, strings.TrimSpace(line))
		}
		return s.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
}
