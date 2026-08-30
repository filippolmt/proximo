// Package skill installs the agent Skill proximo ships, and reports how every
// installed copy stands against the binary that would write it.
//
// The Skill is compiled into the CLI, so its version is the CLI's version: a
// skill describing labels the installed binary does not implement is worse than
// no skill, because it is confidently wrong. A copy this package wrote carries
// a marker file beside its SKILL.md; the absence of that marker is itself the
// answer — the copy came from a channel proximo does not manage, and is left
// alone. See docs/adr/0005-the-agent-skill-ships-in-the-cli.md.
package skill

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/filippolmt/proximo/internal/platform"
	"github.com/filippolmt/proximo/internal/version"
	"github.com/filippolmt/proximo/skills"
)

// Name is the Skill's directory name, under every agent's `skills/`.
const Name = "proximo"

// MarkerName is the side file written beside the installed SKILL.md. Its
// presence marks a Managed copy; its contents say which version wrote the copy
// and what the copy contained when it was written.
const MarkerName = ".proximo-skill.json"

// Agent is a coding agent that reads the Skill. Both read the identical
// format, which is why there is one Skill and not one per agent.
type Agent string

const (
	// Claude is Claude Code, whose skills live under `.claude/skills`.
	Claude Agent = "claude"
	// Codex is OpenAI Codex, whose skills live under `$CODEX_HOME/skills`.
	Codex Agent = "codex"
)

// Agents is every agent proximo knows how to install for, in the order they
// are reported.
var Agents = []Agent{Claude, Codex}

// Scope is where a copy is written: into the repository, where a team shares
// one answer, or into the user's home, where it follows the developer.
type Scope string

const (
	// Project is the repository proximo is being run from. A write at this
	// scope produces a tracked diff, which is announced rather than made
	// quietly.
	Project Scope = "project"
	// Global is the agent's per-user configuration directory.
	Global Scope = "global"
)

// ErrNoRepo is returned when project scope was asked for outside a git
// repository. It is deliberately an error and never a fallback to global: a
// copy silently written somewhere else is a copy nobody can find.
var ErrNoRepo = errors.New("not inside a git repository, so there is no project to install into (use --scope global)")

// Dest is one place the Skill can live.
type Dest struct {
	Agent Agent
	Scope Scope
	// Dir is the Skill's own directory — the one holding SKILL.md.
	Dir string
}

// State is how an installed copy stands against the Skill in this binary.
type State string

const (
	// Absent means nothing is installed at the destination.
	Absent State = "absent"
	// Current means a Managed copy matching this binary, untouched.
	Current State = "current"
	// Stale means a Managed copy, untouched, written by another version.
	Stale State = "stale"
	// Modified means a Managed copy whose content no longer matches what
	// proximo wrote. It is never overwritten without --force.
	Modified State = "modified"
	// Unmanaged means a copy with no marker: a marketplace install, or one
	// made by an agent's own skill-installer. proximo neither updates nor
	// removes it.
	Unmanaged State = "unmanaged"
)

// Copy is one destination and what is actually there.
type Copy struct {
	Dest
	State State
	// Version is what the marker records, empty when there is no marker.
	Version string
}

// Marker is the side file's content.
type Marker struct {
	// Version is the CLI that wrote the copy.
	Version string `json:"version"`
	// Hash is a digest of what it wrote, so a later run can tell a copy it may
	// refresh from one somebody has edited.
	Hash string `json:"hash"`
}

// Root resolves the repository proximo is being run from, walking up from the
// working directory. It asks the filesystem rather than git: the answer is a
// directory containing `.git`, which needs no subprocess and no git on PATH.
func Root() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ErrNoRepo
		}
		dir = parent
	}
}

// base is the agent's configuration directory at the given scope: the parent of
// its `skills/`.
func base(a Agent, s Scope) (string, error) {
	if s == Project {
		root, err := Root()
		if err != nil {
			return "", err
		}
		return filepath.Join(root, "."+string(a)), nil
	}
	if a == Codex {
		if home := os.Getenv("CODEX_HOME"); home != "" {
			return home, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "."+string(a)), nil
}

// Resolve returns the destination for one agent at one scope.
func Resolve(a Agent, s Scope) (Dest, error) {
	dir, err := base(a, s)
	if err != nil {
		return Dest{}, err
	}
	return Dest{Agent: a, Scope: s, Dir: filepath.Join(dir, "skills", Name)}, nil
}

// Detected returns the agents present here, so `skill install` with no --agent
// installs for what is actually used. An agent counts as present when it has a
// configuration directory at either scope, or its command is on PATH: any one
// of the three means somebody runs it here.
//
// The project scope is consulted as well as the home one, because the default
// install *is* project-scoped: a repository that already carries `.claude/` is
// the plainest evidence the agent is used, and refusing on a machine whose home
// has none would ignore the very directory about to be written to.
func Detected() []Agent {
	var found []Agent
	for _, a := range Agents {
		if configured(a, Global) || configured(a, Project) || platform.Has(string(a)) {
			found = append(found, a)
		}
	}
	return found
}

// configured reports whether an agent has a configuration directory at a scope.
// Outside a repository the project scope resolves to nothing, which is an
// absence like any other.
func configured(a Agent, s Scope) bool {
	dir, err := base(a, s)
	if err != nil {
		return false
	}
	_, err = os.Stat(dir)
	return err == nil
}

// Files is the Skill's content, by path relative to the Skill directory.
type Files map[string][]byte

// Embedded returns the Skill compiled into this binary.
func Embedded() (Files, error) {
	files := Files{}
	err := fs.WalkDir(skills.FS, Name, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, readErr := skills.FS.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(Name, path)
		if relErr != nil {
			return relErr
		}
		files[filepath.ToSlash(rel)] = data
		return nil
	})
	return files, err
}

// read returns what is on disk at a destination, excluding the marker: the
// marker records a digest of the Skill, and a digest that included the file
// carrying it could never be computed before writing it.
func read(dir string) (Files, error) {
	files := Files{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if rel == MarkerName {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		files[rel] = data
		return nil
	})
	return files, err
}

// Hash digests a Skill tree: paths and contents both, in sorted order, so two
// runs on the same content always agree and a renamed file is a different tree.
func Hash(files Files) string {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	sum := sha256.New()
	for _, p := range paths {
		fmt.Fprintf(sum, "%s\x00%d\x00", p, len(files[p]))
		sum.Write(files[p])
	}
	return hex.EncodeToString(sum.Sum(nil))
}

// Inspect reports what is at a destination, given the digest of the Skill this
// binary would write.
func Inspect(d Dest, want string) Copy {
	c := Copy{Dest: d, State: Absent}
	if _, err := os.Stat(filepath.Join(d.Dir, "SKILL.md")); err != nil {
		return c
	}

	data, err := os.ReadFile(filepath.Join(d.Dir, MarkerName))
	if err != nil {
		c.State = Unmanaged
		return c
	}
	var m Marker
	if json.Unmarshal(data, &m) != nil || m.Hash == "" {
		c.State = Unmanaged
		return c
	}
	c.Version = m.Version

	files, err := read(d.Dir)
	switch {
	case err != nil || Hash(files) != m.Hash:
		c.State = Modified
	case m.Hash != want || m.Version != version.Version:
		c.State = Stale
	default:
		c.State = Current
	}
	return c
}

// Survey reports every copy proximo can see: both agents, both scopes, minus
// the project scope when there is no repository to look in. It is what the
// `agent-skill` Check reads and what auto-update works from.
func Survey() ([]Copy, error) {
	files, err := Embedded()
	if err != nil {
		return nil, err
	}
	want := Hash(files)

	var copies []Copy
	// One directory is reported once. The two scopes collapse onto the same
	// path when the home directory is itself a git repository — a dotfiles
	// checkout — and a copy counted twice would be refreshed twice, removed
	// twice, and reported as two.
	seen := map[string]bool{}
	for _, s := range []Scope{Project, Global} {
		for _, a := range Agents {
			d, err := Resolve(a, s)
			if errors.Is(err, ErrNoRepo) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if seen[d.Dir] {
				continue
			}
			seen[d.Dir] = true
			copies = append(copies, Inspect(d, want))
		}
	}
	return copies, nil
}

// Action is what a run decided to do about one copy.
type Action string

const (
	// Install writes a copy where there was none.
	Install Action = "install"
	// Refresh brings an untouched Managed copy level with this binary.
	Refresh Action = "refresh"
	// Overwrite replaces an edited copy. Only --force produces it.
	Overwrite Action = "overwrite"
	// Keep leaves a copy that is already level with this binary.
	Keep Action = "keep"
	// SkipModified leaves an edited copy alone and reports it.
	SkipModified Action = "skip (edited locally)"
	// SkipUnmanaged leaves a copy proximo did not write alone.
	SkipUnmanaged Action = "skip (not installed by proximo)"
	// Delete removes a Managed copy that is intact.
	Delete Action = "remove"
)

// Touches reports whether an action changes the filesystem. Removing is one of
// them, which is why it is not called Writes.
func (a Action) Touches() bool {
	return a == Install || a == Refresh || a == Overwrite || a == Delete
}

// Step is one copy and what will be done to it.
type Step struct {
	Copy
	Action Action
}

// rule is what one State means to a run, in both directions.
type rule struct {
	// install and uninstall are what each command does with the copy when the
	// developer has not asked to override anything.
	install, uninstall Action
	// forcible marks the states --force may act on. A copy proximo did not
	// write is never among them: no flag makes somebody else's file ours.
	forcible bool
	// reason says why a copy was left alone, and is empty for the states that
	// were not.
	reason string
}

// states is the whole policy, in one table, so install and uninstall can never
// disagree about what a State means and a new one cannot be added to half of
// it. A State missing from the table is inert by construction — it is neither
// written nor removed.
var states = map[State]rule{
	Absent:    {install: Install, uninstall: Keep},
	Current:   {install: Keep, uninstall: Delete, forcible: true},
	Stale:     {install: Refresh, uninstall: Delete},
	Modified:  {install: SkipModified, uninstall: SkipModified, forcible: true, reason: "it was edited after proximo wrote it"},
	Unmanaged: {install: SkipUnmanaged, uninstall: SkipUnmanaged, reason: "proximo did not install it"},
}

// Managed reports whether proximo wrote this copy and can therefore speak for
// its version.
func (s State) Managed() bool {
	return s == Current || s == Stale || s == Modified
}

// Reason says why a copy in this state is left alone, or "" for a state that
// is not.
func (s State) Reason() string { return states[s].reason }

// Forcible reports whether --force may act on a copy in this state, which is
// what decides whether a report has an override to offer.
func (s State) Forcible() bool { return states[s].forcible }

// Decide maps a copy's state to the action an install would take.
func Decide(c Copy, force bool) Action {
	r, ok := states[c.State]
	switch {
	case !ok:
		return Keep
	case force && r.forcible:
		return Overwrite
	}
	return r.install
}

// DecideRemove maps a copy's state to the action an uninstall would take.
// Uninstall keeps the promise that install is reversible, without extending it
// into deleting work proximo did not write.
func DecideRemove(c Copy, force bool) Action {
	r, ok := states[c.State]
	switch {
	case !ok:
		return Keep
	case force && r.forcible:
		return Delete
	}
	return r.uninstall
}

// Plan pairs each destination with the action an install would take.
func Plan(dests []Dest, force bool) ([]Step, error) {
	return plan(dests, force, Decide)
}

// PlanRemove pairs each destination with the action an uninstall would take.
func PlanRemove(dests []Dest, force bool) ([]Step, error) {
	return plan(dests, force, DecideRemove)
}

// plan reads every destination once and asks the given decision what to do
// with it. The two directions differ only in that decision.
func plan(dests []Dest, force bool, decide func(Copy, bool) Action) ([]Step, error) {
	files, err := Embedded()
	if err != nil {
		return nil, err
	}
	want := Hash(files)

	steps := make([]Step, 0, len(dests))
	for _, d := range dests {
		c := Inspect(d, want)
		steps = append(steps, Step{Copy: c, Action: decide(c, force)})
	}
	return steps, nil
}

// Apply performs the writing steps, leaving every other one alone.
func Apply(steps []Step) error {
	files, err := Embedded()
	if err != nil {
		return err
	}
	for _, s := range steps {
		switch s.Action {
		case Delete:
			if err := os.RemoveAll(s.Dir); err != nil {
				return err
			}
		case Install, Refresh, Overwrite:
			if err := Write(s.Dir, files); err != nil {
				return err
			}
		}
	}
	return nil
}

// Write installs the Skill into dir and marks it Managed. The marker is written
// last: a run interrupted midway leaves a copy with no marker, which the next
// run treats as unmanaged and reports rather than a copy it silently trusts.
func Write(dir string, files Files) error {
	// Stale files from a previous version are removed rather than left beside
	// the new ones: a reference the Skill no longer links to would otherwise
	// linger forever, and the digest would never match again.
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	for path, data := range files {
		dest := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			return err
		}
	}
	marker, err := json.MarshalIndent(Marker{Version: version.Version, Hash: Hash(files)}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, MarkerName), append(marker, '\n'), 0o644)
}

// ParseAgents resolves the --agent flag. An empty value means every agent
// detected on this host, which is the default; "all" means both, detected or
// not, for a developer setting a machine up before installing the agent.
func ParseAgents(flag string) ([]Agent, error) {
	switch flag {
	case "":
		found := Detected()
		if len(found) == 0 {
			return nil, fmt.Errorf("no coding agent detected on this host; name one with --agent claude|codex|all")
		}
		return found, nil
	case "all":
		return Agents, nil
	}

	var picked []Agent
	for _, name := range strings.Split(flag, ",") {
		a := Agent(strings.TrimSpace(name))
		if !slices.Contains(Agents, a) {
			return nil, fmt.Errorf("unknown agent %q: use claude, codex, or all", name)
		}
		if !slices.Contains(picked, a) {
			picked = append(picked, a)
		}
	}
	return picked, nil
}

// ParseScope resolves the --scope flag.
func ParseScope(flag string) (Scope, error) {
	switch s := Scope(flag); s {
	case Project, Global:
		return s, nil
	default:
		return "", fmt.Errorf("unknown scope %q: use project or global", flag)
	}
}

// Dirs renders the copies a report is about, in the paths a developer types.
func Dirs(copies []Copy) string {
	dirs := make([]string, len(copies))
	for i, c := range copies {
		dirs[i] = Short(c.Dir)
	}
	return strings.Join(dirs, ", ")
}

// Command is the `proximo skill <verb>` invocation that reaches these copies.
// Both subcommands default to project scope, so a set that lives only under the
// developer's home must carry --scope global — otherwise the command would act
// somewhere else entirely and leave these exactly as they were.
//
// A set spanning both scopes takes two runs: this names the project one, and
// whatever reports next names what is left. Naming one command that reaches
// half the set is the alternative, and it is worse.
func Command(verb string, copies []Copy, force bool) string {
	cmd := "proximo skill " + verb
	if force {
		cmd += " --force"
	}
	for _, c := range copies {
		if c.Scope == Project {
			return cmd
		}
	}
	return cmd + " --scope global"
}

// Short renders a path the way a developer reads it back — and types it when
// they go and look: relative to the working directory when it is under it, so a
// project copy reads as `.claude/skills/proximo`, exactly as it appears in
// `git status`.
//
// Never from the home directory or an ancestor of it, though. Standing in
// `$HOME`, a global copy would render as `.claude/skills/proximo` — the shape
// of a project copy, beside a Remedy naming `--scope global`; standing in `/`,
// it would render as a bare `home/you/…` that resolves nowhere else. Both are
// `~/.claude/skills/proximo` instead, which is true wherever it is read.
func Short(path string) string {
	home, homeErr := os.UserHomeDir()
	cwd, cwdErr := os.Getwd()

	if cwdErr == nil && (homeErr != nil || !under(home, cwd)) {
		if rel, err := filepath.Rel(cwd, path); err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	if homeErr != nil || home == "" {
		return path
	}
	rel, err := filepath.Rel(home, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return filepath.Join("~", rel)
}

// under reports whether path is dir itself or sits beneath it.
func under(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && !strings.HasPrefix(rel, "..")
}
