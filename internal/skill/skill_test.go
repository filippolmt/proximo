package skill

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/filippolmt/proximo/internal/version"
)

func TestEmbeddedSkillIsShipped(t *testing.T) {
	files, err := Embedded()
	if err != nil {
		t.Fatalf("embedded: %v", err)
	}
	if _, ok := files["SKILL.md"]; !ok {
		t.Fatal("the embedded skill has no SKILL.md")
	}
	if len(files) < 2 {
		t.Fatalf("the embedded skill carries no references: %v", files)
	}
}

// The digest covers paths as well as contents, so a renamed reference is a
// different tree and a copy holding the old name is not mistaken for current.
func TestHashCoversPathsAndContents(t *testing.T) {
	base := Files{"SKILL.md": []byte("a"), "references/x.md": []byte("b")}
	same := Files{"references/x.md": []byte("b"), "SKILL.md": []byte("a")}
	renamed := Files{"SKILL.md": []byte("a"), "references/y.md": []byte("b")}
	edited := Files{"SKILL.md": []byte("a"), "references/x.md": []byte("c")}

	if Hash(base) != Hash(same) {
		t.Error("the digest depends on map iteration order")
	}
	if Hash(base) == Hash(renamed) {
		t.Error("a renamed file produced the same digest")
	}
	if Hash(base) == Hash(edited) {
		t.Error("edited content produced the same digest")
	}
}

// A boundary between two files must not be movable without changing the
// digest: "ab" in one file and "a"+"b" split across two are different trees.
func TestHashSeparatesFiles(t *testing.T) {
	joined := Files{"a.md": []byte("xy")}
	split := Files{"a.md": []byte("x"), "a.mdy": nil}
	if Hash(joined) == Hash(split) {
		t.Error("file boundaries are not part of the digest")
	}
}

func TestWriteMarksTheCopyManaged(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "proximo")
	files := Files{"SKILL.md": []byte("hello"), "references/x.md": []byte("ref")}
	if err := Write(dir, files); err != nil {
		t.Fatalf("write: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, MarkerName))
	if err != nil {
		t.Fatalf("marker: %v", err)
	}
	var m Marker
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("marker is not JSON: %v", err)
	}
	if m.Version != version.Version {
		t.Errorf("marker records version %q, want %q", m.Version, version.Version)
	}
	if m.Hash != Hash(files) {
		t.Error("marker records a digest of something other than what was written")
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "references", "x.md")); string(got) != "ref" {
		t.Errorf("reference written as %q", got)
	}
}

// A file the new version no longer ships must not survive the write, or the
// copy would never match its own digest again.
func TestWriteDropsFilesTheVersionNoLongerShips(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "proximo")
	if err := Write(dir, Files{"SKILL.md": []byte("v1"), "references/gone.md": []byte("old")}); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	if err := Write(dir, Files{"SKILL.md": []byte("v2")}); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "references", "gone.md")); err == nil {
		t.Error("a reference dropped by the new version is still on disk")
	}
}

func TestInspectStates(t *testing.T) {
	files := Files{"SKILL.md": []byte("current")}
	want := Hash(files)

	t.Run("absent", func(t *testing.T) {
		d := Dest{Dir: filepath.Join(t.TempDir(), "nothing")}
		if got := Inspect(d, want).State; got != Absent {
			t.Errorf("state %q, want %q", got, Absent)
		}
	})

	t.Run("current", func(t *testing.T) {
		d := Dest{Dir: filepath.Join(t.TempDir(), "proximo")}
		mustWrite(t, d.Dir, files)
		if got := Inspect(d, want).State; got != Current {
			t.Errorf("state %q, want %q", got, Current)
		}
	})

	// No marker: a marketplace install, or Codex's own skill-installer. proximo
	// neither updates nor removes it, and says so rather than claiming a
	// version it cannot know.
	t.Run("unmanaged", func(t *testing.T) {
		d := Dest{Dir: filepath.Join(t.TempDir(), "proximo")}
		mustWrite(t, d.Dir, files)
		if err := os.Remove(filepath.Join(d.Dir, MarkerName)); err != nil {
			t.Fatal(err)
		}
		if got := Inspect(d, want).State; got != Unmanaged {
			t.Errorf("state %q, want %q", got, Unmanaged)
		}
	})

	t.Run("stale", func(t *testing.T) {
		d := Dest{Dir: filepath.Join(t.TempDir(), "proximo")}
		mustWrite(t, d.Dir, Files{"SKILL.md": []byte("older")})
		c := Inspect(d, want)
		if c.State != Stale {
			t.Errorf("state %q, want %q", c.State, Stale)
		}
		if c.Version != version.Version {
			t.Errorf("version %q, want the one the marker records", c.Version)
		}
	})

	// Edited after proximo wrote it: the content belongs to whoever changed it.
	t.Run("modified", func(t *testing.T) {
		d := Dest{Dir: filepath.Join(t.TempDir(), "proximo")}
		mustWrite(t, d.Dir, files)
		if err := os.WriteFile(filepath.Join(d.Dir, "SKILL.md"), []byte("hand-edited"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := Inspect(d, want).State; got != Modified {
			t.Errorf("state %q, want %q", got, Modified)
		}
	})

	// A marker that is not readable JSON says nothing about what is there, so
	// the copy is unmanaged rather than assumed intact.
	t.Run("unreadable marker", func(t *testing.T) {
		d := Dest{Dir: filepath.Join(t.TempDir(), "proximo")}
		mustWrite(t, d.Dir, files)
		if err := os.WriteFile(filepath.Join(d.Dir, MarkerName), []byte("{"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := Inspect(d, want).State; got != Unmanaged {
			t.Errorf("state %q, want %q", got, Unmanaged)
		}
	})
}

// A copy whose content still matches its marker but whose version does not
// match the binary is refreshed, so the Skill's own first step never reports a
// version proximo no longer ships.
func TestInspectStaleOnVersionAlone(t *testing.T) {
	files := Files{"SKILL.md": []byte("unchanged")}
	d := Dest{Dir: filepath.Join(t.TempDir(), "proximo")}
	mustWrite(t, d.Dir, files)

	marker, err := json.Marshal(Marker{Version: "0.0.1", Hash: Hash(files)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.Dir, MarkerName), marker, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Inspect(d, Hash(files)).State; got != Stale {
		t.Errorf("state %q, want %q", got, Stale)
	}
}

func TestDecide(t *testing.T) {
	for _, tc := range []struct {
		state           State
		want, wantForce Action
	}{
		{Absent, Install, Install},
		{Stale, Refresh, Refresh},
		{Current, Keep, Overwrite},
		{Modified, SkipModified, Overwrite},
		{Unmanaged, SkipUnmanaged, SkipUnmanaged},
	} {
		if got := Decide(Copy{State: tc.state}, false); got != tc.want {
			t.Errorf("Decide(%s) = %q, want %q", tc.state, got, tc.want)
		}
		if got := Decide(Copy{State: tc.state}, true); got != tc.wantForce {
			t.Errorf("Decide(%s, force) = %q, want %q", tc.state, got, tc.wantForce)
		}
	}
}

// Uninstall is reversible without extending into deleting work proximo did not
// write: an unmanaged copy is never removed, not even with --force.
func TestDecideRemove(t *testing.T) {
	for _, tc := range []struct {
		state           State
		want, wantForce Action
	}{
		{Absent, Keep, Keep},
		{Current, Delete, Delete},
		{Stale, Delete, Delete},
		{Modified, SkipModified, Delete},
		{Unmanaged, SkipUnmanaged, SkipUnmanaged},
	} {
		if got := DecideRemove(Copy{State: tc.state}, false); got != tc.want {
			t.Errorf("DecideRemove(%s) = %q, want %q", tc.state, got, tc.want)
		}
		if got := DecideRemove(Copy{State: tc.state}, true); got != tc.wantForce {
			t.Errorf("DecideRemove(%s, force) = %q, want %q", tc.state, got, tc.wantForce)
		}
	}
}

func TestResolveDestinations(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")

	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repo)

	for _, tc := range []struct {
		agent Agent
		scope Scope
		want  string
	}{
		{Claude, Global, filepath.Join(home, ".claude", "skills", "proximo")},
		{Codex, Global, filepath.Join(home, ".codex", "skills", "proximo")},
		{Claude, Project, filepath.Join(repo, ".claude", "skills", "proximo")},
		{Codex, Project, filepath.Join(repo, ".codex", "skills", "proximo")},
	} {
		d, err := Resolve(tc.agent, tc.scope)
		if err != nil {
			t.Fatalf("resolve %s/%s: %v", tc.agent, tc.scope, err)
		}
		if d.Dir != tc.want {
			t.Errorf("%s/%s resolved to %s, want %s", tc.agent, tc.scope, d.Dir, tc.want)
		}
	}
}

func TestCodexHomeOverridesTheDefault(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	elsewhere := t.TempDir()
	t.Setenv("CODEX_HOME", elsewhere)

	d, err := Resolve(Codex, Global)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if want := filepath.Join(elsewhere, "skills", "proximo"); d.Dir != want {
		t.Errorf("resolved to %s, want %s", d.Dir, want)
	}
}

// Project scope outside a repository is an error naming the alternative, never
// a silent fallback to the home directory.
func TestProjectScopeOutsideARepositoryIsAnError(t *testing.T) {
	t.Chdir(t.TempDir())
	if _, err := Resolve(Claude, Project); err == nil {
		t.Fatal("resolve succeeded outside a repository")
	} else if !strings.Contains(err.Error(), "--scope global") {
		t.Errorf("error %q does not name the alternative", err)
	}
}

func TestSurveySkipsProjectScopeOutsideARepository(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", "")
	t.Chdir(t.TempDir())

	copies, err := Survey()
	if err != nil {
		t.Fatalf("survey: %v", err)
	}
	if len(copies) != len(Agents) {
		t.Fatalf("survey reported %d copies, want the %d global ones", len(copies), len(Agents))
	}
	for _, c := range copies {
		if c.Scope != Global {
			t.Errorf("survey reported a %s copy outside a repository", c.Scope)
		}
	}
}

func TestParseAgents(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	t.Setenv("CODEX_HOME", "")
	// Outside a repository, so the project scope contributes no detection —
	// the package's own directory is inside one that carries `.claude/`.
	t.Chdir(t.TempDir())

	if got, err := ParseAgents("all"); err != nil || len(got) != 2 {
		t.Errorf("ParseAgents(all) = %v, %v", got, err)
	}
	if got, err := ParseAgents("codex,claude,codex"); err != nil || len(got) != 2 {
		t.Errorf("ParseAgents deduplicates: %v, %v", got, err)
	}
	if _, err := ParseAgents("cursor"); err == nil {
		t.Error("ParseAgents accepted an unknown agent")
	}
	// Nothing installed and nothing named: an error, not an empty no-op that
	// reports success while writing nothing.
	if _, err := ParseAgents(""); err == nil {
		t.Error("ParseAgents accepted a host with no agent")
	}
}

func TestDetectedFindsAnAgentByItsConfigDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("CODEX_HOME", "")
	t.Chdir(t.TempDir())
	if err := os.Mkdir(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := Detected()
	if len(got) != 1 || got[0] != Claude {
		t.Errorf("Detected() = %v, want [claude]", got)
	}
}

// The default install is project-scoped, so a repository already carrying
// `.codex/` is evidence the agent is used — even on a machine whose home has
// nothing and whose PATH holds no such command.
func TestDetectedFindsAnAgentByTheProjectItIsRunIn(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	t.Setenv("CODEX_HOME", "")

	repo := t.TempDir()
	for _, dir := range []string{".git", ".codex"} {
		if err := os.Mkdir(filepath.Join(repo, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(repo)

	got := Detected()
	if len(got) != 1 || got[0] != Codex {
		t.Errorf("Detected() = %v, want [codex]", got)
	}
}

func TestParseScope(t *testing.T) {
	if _, err := ParseScope("project"); err != nil {
		t.Errorf("project rejected: %v", err)
	}
	if _, err := ParseScope("user"); err == nil {
		t.Error("ParseScope accepted an unknown scope")
	}
}

func mustWrite(t *testing.T, dir string, files Files) {
	t.Helper()
	if err := Write(dir, files); err != nil {
		t.Fatalf("write: %v", err)
	}
}

// A State the policy table does not carry is inert: neither written nor
// removed. Without that, adding one would silently join the overwrite branch.
func TestUnknownStateIsInert(t *testing.T) {
	unknown := Copy{State: State("invented")}
	for _, force := range []bool{false, true} {
		if got := Decide(unknown, force); got.Touches() {
			t.Errorf("Decide(unknown, force=%v) = %q, which changes the filesystem", force, got)
		}
		if got := DecideRemove(unknown, force); got.Touches() {
			t.Errorf("DecideRemove(unknown, force=%v) = %q, which changes the filesystem", force, got)
		}
	}
}

// No flag makes somebody else's file ours: --force never reaches a copy proximo
// did not write, in either direction.
func TestForceNeverTouchesAnUnmanagedCopy(t *testing.T) {
	c := Copy{State: Unmanaged}
	if got := Decide(c, true); got.Touches() {
		t.Errorf("Decide(unmanaged, force) = %q", got)
	}
	if got := DecideRemove(c, true); got.Touches() {
		t.Errorf("DecideRemove(unmanaged, force) = %q", got)
	}
}

func TestForcible(t *testing.T) {
	for _, state := range []State{Current, Modified} {
		if !state.Forcible() {
			t.Errorf("--force cannot act on %s, so a report has no override to offer", state)
		}
	}
	// No flag makes somebody else's file ours.
	if Unmanaged.Forcible() {
		t.Error("--force may act on an unmanaged copy")
	}
}

func TestStateReason(t *testing.T) {
	for _, state := range []State{Modified, Unmanaged} {
		if state.Reason() == "" {
			t.Errorf("%s is left alone without saying why", state)
		}
	}
	for _, state := range []State{Absent, Current, Stale} {
		if state.Reason() != "" {
			t.Errorf("%s is not left alone, but gives a reason: %q", state, state.Reason())
		}
	}
}

func TestManaged(t *testing.T) {
	for _, state := range []State{Current, Stale, Modified} {
		if !state.Managed() {
			t.Errorf("%s is a copy proximo wrote, but does not count as managed", state)
		}
	}
	for _, state := range []State{Absent, Unmanaged} {
		if state.Managed() {
			t.Errorf("%s counts as managed", state)
		}
	}
}

// Both subcommands default to project scope, so a set living only under the
// home directory must carry the scope that reaches it.
func TestCommandNamesTheScopeThatReaches(t *testing.T) {
	global := []Copy{{Dest: Dest{Scope: Global}}}
	both := []Copy{{Dest: Dest{Scope: Global}}, {Dest: Dest{Scope: Project}}}

	for _, tc := range []struct {
		verb   string
		copies []Copy
		force  bool
		want   string
	}{
		{"install", global, false, "proximo skill install --scope global"},
		{"install", global, true, "proximo skill install --force --scope global"},
		{"install", both, false, "proximo skill install"},
		{"uninstall", []Copy{{Dest: Dest{Scope: Project}}}, true, "proximo skill uninstall --force"},
	} {
		if got := Command(tc.verb, tc.copies, tc.force); got != tc.want {
			t.Errorf("Command(%s) = %q, want %q", tc.verb, got, tc.want)
		}
	}
}
