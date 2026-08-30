package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/filippolmt/proximo/internal/skill"
	"github.com/filippolmt/proximo/internal/version"
)

// repo puts the test in a fresh home and a fresh git repository, which is what
// the two scopes resolve against.
func repo(t *testing.T) (home, root string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")

	root = t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	return home, root
}

func runCmd(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("%v: %v\n%s", args, err, out.String())
	}
	return out.String()
}

func TestSkillInstallWritesBothAgentsAtProjectScope(t *testing.T) {
	_, root := repo(t)

	out := runCmd(t, "skill", "install", "--agent", "all")

	for _, agent := range []string{".claude", ".codex"} {
		path := filepath.Join(root, agent, "skills", "proximo", "SKILL.md")
		if _, err := os.Stat(path); err != nil {
			t.Errorf("no SKILL.md at %s: %v", path, err)
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(path), skill.MarkerName)); err != nil {
			t.Errorf("no marker beside %s: %v", path, err)
		}
	}
	// A project-scope write is a tracked diff, and is announced as one.
	if !strings.Contains(out, "commit the diff") {
		t.Errorf("project write was not announced as a diff:\n%s", out)
	}
}

func TestSkillInstallDryRunWritesNothing(t *testing.T) {
	_, root := repo(t)

	out := runCmd(t, "skill", "install", "--agent", "claude", "--dry-run")

	if _, err := os.Stat(filepath.Join(root, ".claude")); err == nil {
		t.Error("--dry-run wrote to the repository")
	}
	if !strings.Contains(out, string(skill.Install)) {
		t.Errorf("the plan does not say what it would do:\n%s", out)
	}
}

// The installed copy is byte-identical to the source in the binary: that is
// what makes the digest, and therefore the whole managed/edited distinction,
// mean anything.
func TestSkillInstallWritesTheEmbeddedSkillVerbatim(t *testing.T) {
	home, _ := repo(t)
	runCmd(t, "skill", "install", "--agent", "claude", "--scope", "global")

	files, err := skill.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".claude", "skills", "proximo")
	for path, want := range files {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(path)))
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s was not written verbatim", path)
		}
	}
}

func TestSkillInstallIsIdempotent(t *testing.T) {
	repo(t)
	runCmd(t, "skill", "install", "--agent", "claude")

	out := runCmd(t, "skill", "install", "--agent", "claude")
	if !strings.Contains(out, "up to date") || !strings.Contains(out, "Nothing to do") {
		t.Errorf("a second install did not report a no-op:\n%s", out)
	}
}

func TestSkillInstallLeavesAnEditedCopyAloneWithoutForce(t *testing.T) {
	_, root := repo(t)
	runCmd(t, "skill", "install", "--agent", "claude")

	edited := filepath.Join(root, ".claude", "skills", "proximo", "SKILL.md")
	if err := os.WriteFile(edited, []byte("our team's own version"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := runCmd(t, "skill", "install", "--agent", "claude")
	if !strings.Contains(out, "--force") {
		t.Errorf("the skip does not name the command that overrides it:\n%s", out)
	}
	if got, _ := os.ReadFile(edited); string(got) != "our team's own version" {
		t.Error("the edited copy was overwritten without --force")
	}

	runCmd(t, "skill", "install", "--agent", "claude", "--force")
	if got, _ := os.ReadFile(edited); string(got) == "our team's own version" {
		t.Error("--force did not overwrite the edited copy")
	}
}

// Project scope outside a repository is an error naming --global, never a
// silent write into the home directory.
func TestSkillInstallOutsideARepositoryRefusesProjectScope(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Chdir(t.TempDir())

	var out bytes.Buffer
	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"skill", "install", "--agent", "claude"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("install succeeded outside a repository")
	}
	if !strings.Contains(err.Error(), "--scope global") {
		t.Errorf("error %q does not name the alternative", err)
	}
}

func TestSkillUninstallRemovesOnlyWhatProximoWrote(t *testing.T) {
	_, root := repo(t)
	runCmd(t, "skill", "install", "--agent", "all")

	// One copy edited by hand, one copy from another channel.
	claude := filepath.Join(root, ".claude", "skills", "proximo")
	codex := filepath.Join(root, ".codex", "skills", "proximo")
	if err := os.WriteFile(filepath.Join(claude, "SKILL.md"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(codex, skill.MarkerName)); err != nil {
		t.Fatal(err)
	}

	runCmd(t, "skill", "uninstall", "--agent", "all")
	for _, dir := range []string{claude, codex} {
		if _, err := os.Stat(filepath.Join(dir, "SKILL.md")); err != nil {
			t.Errorf("%s was removed although proximo may not touch it", dir)
		}
	}

	// A copy proximo wrote and nobody touched is removed.
	runCmd(t, "skill", "install", "--agent", "claude", "--force")
	runCmd(t, "skill", "uninstall", "--agent", "claude")
	if _, err := os.Stat(claude); err == nil {
		t.Error("an intact managed copy survived uninstall")
	}
}

// Auto-update brings a copy behind the binary level, and never conjures one
// where the developer installed none.
func TestRefreshSkillsUpdatesManagedCopiesOnly(t *testing.T) {
	home, root := repo(t)
	runCmd(t, "skill", "install", "--agent", "claude")

	dir := filepath.Join(root, ".claude", "skills", "proximo")
	stale := skill.Files{"SKILL.md": []byte("an older version's skill")}
	if err := skill.Write(dir, stale); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, skill.MarkerName),
		[]byte(`{"version":"0.0.1","hash":"`+skill.Hash(stale)+`"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	refreshSkills(&out)

	got, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) == "an older version's skill" {
		t.Error("a stale managed copy was not refreshed")
	}
	if !strings.Contains(out.String(), version.Version) {
		t.Errorf("the refresh did not say which version it wrote:\n%s", out.String())
	}
	// A project copy is a tracked file wherever the change came from, so
	// auto-update announces the diff exactly as the typed command does.
	if !strings.Contains(out.String(), "commit the diff") {
		t.Errorf("auto-update changed a tracked file without announcing it:\n%s", out.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "proximo")); err == nil {
		t.Error("auto-update installed a copy where there was none")
	}
}

func TestRefreshSkillsIsSilentWhenEverythingIsLevel(t *testing.T) {
	repo(t)
	runCmd(t, "skill", "install", "--agent", "claude")

	var out bytes.Buffer
	refreshSkills(&out)
	if out.Len() != 0 {
		t.Errorf("auto-update spoke with nothing to say:\n%s", out.String())
	}
}

// A copy edited after proximo wrote it is named by the lifecycle commands too,
// not only by the typed one — and is never refreshed out from under the team.
func TestRefreshSkillsReportsAnEditedCopyAndLeavesIt(t *testing.T) {
	_, root := repo(t)
	runCmd(t, "skill", "install", "--agent", "claude")

	edited := filepath.Join(root, ".claude", "skills", "proximo", "SKILL.md")
	if err := os.WriteFile(edited, []byte("our team's own version"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	refreshSkills(&out)

	if !strings.Contains(out.String(), "--force") {
		t.Errorf("the skip does not name the command that overrides it:\n%s", out.String())
	}
	if got, _ := os.ReadFile(edited); string(got) != "our team's own version" {
		t.Error("auto-update overwrote a copy that was edited after proximo wrote it")
	}
}

// Uninstall deletes a tracked file out of a team's repository, which is a diff
// to review like any other.
func TestRemoveSkillsAnnouncesTheTrackedDiff(t *testing.T) {
	repo(t)
	runCmd(t, "skill", "install", "--agent", "claude")

	var out bytes.Buffer
	removeSkills(&out)

	if !strings.Contains(out.String(), "removed") {
		t.Fatalf("nothing was removed:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "commit the diff") {
		t.Errorf("a tracked file was deleted without announcing it:\n%s", out.String())
	}
}
