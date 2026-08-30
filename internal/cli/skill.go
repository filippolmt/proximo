package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/filippolmt/proximo/internal/skill"
	"github.com/filippolmt/proximo/internal/version"
	"github.com/spf13/cobra"
)

func newSkillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Install the proximo agent Skill into the coding agents on this host",
		Long: "The Skill teaches a coding agent how to expose a container through " +
			"the proximo labels and how to diagnose one that is broken. It is " +
			"compiled into this binary, so an installed copy is always the one " +
			"that matches the CLI you are running.",
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(newSkillInstallCmd(), newSkillUninstallCmd())
	return cmd
}

// skillFlags are what both subcommands take: which copies to act on, whether to
// stop at the plan, and whether to act on copies that were edited by hand.
type skillFlags struct {
	agent  string
	scope  string
	dryRun bool
	force  bool
}

func (f *skillFlags) register(cmd *cobra.Command, forceHelp string) {
	cmd.Flags().StringVar(&f.agent, "agent", "",
		"Agent(s) to act on: claude, codex, or all (default: every agent detected)")
	cmd.Flags().StringVar(&f.scope, "scope", string(skill.Project),
		"Where the copy lives: project (this repository) or global (your home)")
	cmd.Flags().BoolVar(&f.dryRun, "dry-run", false, "Print the plan and stop")
	cmd.Flags().BoolVar(&f.force, "force", false, forceHelp)
}

// dests resolves the destinations the flags name.
func (f *skillFlags) dests() ([]skill.Dest, error) {
	agents, err := skill.ParseAgents(f.agent)
	if err != nil {
		return nil, err
	}
	scope, err := skill.ParseScope(f.scope)
	if err != nil {
		return nil, err
	}
	dests := make([]skill.Dest, 0, len(agents))
	for _, a := range agents {
		d, err := skill.Resolve(a, scope)
		if err != nil {
			return nil, err
		}
		dests = append(dests, d)
	}
	return dests, nil
}

func newSkillInstallCmd() *cobra.Command {
	var f skillFlags
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Write the Skill where your agents will read it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dests, err := f.dests()
			if err != nil {
				return err
			}
			steps, err := skill.Plan(dests, f.force)
			if err != nil {
				return err
			}
			return runSkill(cmd.OutOrStdout(), steps, f.dryRun, "overwrite")
		},
	}
	f.register(cmd, "Overwrite a copy that was edited after proximo wrote it")
	return cmd
}

func newSkillUninstallCmd() *cobra.Command {
	var f skillFlags
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove the Skill copies proximo installed",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			dests, err := f.dests()
			if err != nil {
				return err
			}
			steps, err := skill.PlanRemove(dests, f.force)
			if err != nil {
				return err
			}
			return runSkill(cmd.OutOrStdout(), steps, f.dryRun, "remove")
		},
	}
	f.register(cmd, "Remove a copy that was edited after proximo wrote it")
	return cmd
}

// runSkill prints the plan, applies it unless this is a dry run, and says what
// a project-scope write means for the repository. The plan is printed before
// anything is written: these are files a team may have to review.
func runSkill(out io.Writer, steps []skill.Step, dryRun bool, forceVerb string) error {
	fmt.Fprintf(out, "Skill: proximo %s\n\n", version.Version)
	printPlan(out, steps, forceVerb)

	if dryRun {
		fmt.Fprintln(out, "\nDry run: nothing was written.")
		return nil
	}
	if !writesAnything(steps) {
		fmt.Fprintln(out, "\nNothing to do.")
		return nil
	}
	if err := skill.Apply(steps); err != nil {
		return err
	}

	fmt.Fprintln(out, "\nDone. Restart your agent session for the change to take effect.")
	announceTracked(out, steps, "")
	return nil
}

// printPlan renders one line per destination: what is there, and what will
// happen to it.
func printPlan(out io.Writer, steps []skill.Step, forceVerb string) {
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, s := range steps {
		fmt.Fprintf(w, "  %s\t%s\t%s\t%s\n", s.Agent, s.Scope, skill.Short(s.Dir), stepDetail(s, forceVerb))
	}
	w.Flush()
}

// stepDetail says what will happen and, where it matters, what stands in the way.
func stepDetail(s skill.Step, forceVerb string) string {
	switch s.Action {
	case skill.Refresh:
		return fmt.Sprintf("refresh (%s -> %s)", s.Version, version.Version)
	case skill.Keep:
		if s.State == skill.Absent {
			return "nothing installed"
		}
		return "up to date"
	case skill.SkipModified:
		return fmt.Sprintf("%s; --force to %s", skill.SkipModified, forceVerb)
	default:
		return string(s.Action)
	}
}

func writesAnything(steps []skill.Step) bool {
	for _, s := range steps {
		if s.Action.Touches() {
			return true
		}
	}
	return false
}

// announceTracked names the project-scope directories a run changed. A copy in
// a repository is a tracked file, and updating one produces a diff the team did
// not ask for — which the ADR answers by announcing the diff rather than by
// declining to make it. That holds wherever the change comes from, so the
// lifecycle commands say it too, not only the one the developer typed.
func announceTracked(out io.Writer, steps []skill.Step, indent string) {
	var dirs []string
	for _, s := range steps {
		if s.Scope == skill.Project && s.Action.Touches() {
			dirs = append(dirs, skill.Short(s.Dir))
		}
	}
	if len(dirs) == 0 {
		return
	}
	fmt.Fprintf(out, "%sThese are tracked files: review and commit the diff under %s.\n",
		indent, strings.Join(dirs, ", "))
}

// reconcileSkills applies one lifecycle decision to every Skill copy proximo can
// see, reports what it did, and names the copies it was not allowed to touch. It
// is called by the commands that already materialise proximo's own artefacts
// onto disk — never by `status`, which is an inventory and must write nothing.
//
// It never fails its caller: by the time it runs, the host is already in the
// state the command promised, and a skill file that could not be written must
// not be reported as a stack that did not come up.
// reconcile is one lifecycle direction applied to every Skill copy proximo can
// see. It is what the commands that already materialise proximo's own artefacts
// onto disk do to the Skill — never `status`, which is an inventory and must
// write nothing.
//
// It never fails its caller: by the time it runs, the host is already in the
// state the command promised, and a skill file that could not be written must
// not be reported as a stack that did not come up.
type reconcile struct {
	// verb is the `proximo skill` subcommand a developer types to override a
	// copy this run may not touch.
	verb string
	// decide is the direction: what this run wants done with one copy.
	decide func(skill.Copy) skill.Action
	// done renders one step that was carried out.
	done func(skill.Step) string
}

func (r reconcile) run(out io.Writer) {
	copies, err := skill.Survey()
	if err != nil {
		return
	}

	var steps []skill.Step
	var blocked []skill.Copy
	for _, c := range copies {
		if a := r.decide(c); a.Touches() {
			steps = append(steps, skill.Step{Copy: c, Action: a})
		} else if c.State.Reason() != "" {
			blocked = append(blocked, c)
		}
	}
	if len(steps) == 0 && len(blocked) == 0 {
		return
	}

	fmt.Fprintln(out, "==> Agent skill")
	// The blocked copies are reported whatever the writes do: they are the ones
	// a write failure has nothing to do with, and losing the line that names
	// them would hide the only copies needing a decision.
	defer func() {
		for _, c := range blocked {
			fmt.Fprintf(out, "    left %s alone: %s%s\n",
				skill.Short(c.Dir), c.State.Reason(), r.override(c))
		}
	}()

	if err := skill.Apply(steps); err != nil {
		fmt.Fprintf(out, "    could not reconcile the agent skill: %v\n", err)
		return
	}
	for _, s := range steps {
		fmt.Fprintln(out, "    "+r.done(s))
	}
	announceTracked(out, steps, "    ")
	fmt.Fprintln(out, "    restart your agent session for the change to take effect")
}

// override is the command that acts on a copy this run left alone, in
// parentheses — and nothing at all for a copy proximo did not write, which no
// flag makes ours.
func (r reconcile) override(c skill.Copy) string {
	if !c.State.Forcible() {
		return ""
	}
	return " (" + skill.Command(r.verb, []skill.Copy{c}, true) + ")"
}

// refreshSkills brings every Managed copy of the Skill level with this binary.
func refreshSkills(out io.Writer) {
	reconcile{
		verb:   "install",
		decide: refresh,
		done: func(s skill.Step) string {
			return fmt.Sprintf("refreshed %s (%s -> %s)", skill.Short(s.Dir), s.Version, version.Version)
		},
	}.run(out)
}

// refresh is auto-update's decision. It never installs where there was no copy:
// a developer who uses no coding agent must not have one appear because they
// started the stack.
func refresh(c skill.Copy) skill.Action {
	if c.State == skill.Stale {
		return skill.Refresh
	}
	return skill.Keep
}

// removeSkills takes back the Skill copies proximo wrote, keeping the promise
// that install is reversible without extending it into deleting work proximo
// did not write: an edited copy and a copy from another channel are listed, not
// removed.
func removeSkills(out io.Writer) {
	reconcile{
		verb:   "uninstall",
		decide: func(c skill.Copy) skill.Action { return skill.DecideRemove(c, false) },
		done:   func(s skill.Step) string { return "removed " + skill.Short(s.Dir) },
	}.run(out)
}
