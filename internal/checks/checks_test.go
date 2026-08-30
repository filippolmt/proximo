package checks

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/filippolmt/proximo/internal/docker"
	"github.com/filippolmt/proximo/internal/skill"
)

// healthyEnv is a machine where every statement holds. It is the fixture the
// suite guards hardest: a diagnostic tool that reports a failure on a healthy
// machine is worse than one that reports nothing.
func healthyEnv() Env {
	return Env{
		TLD:            "test",
		CLIVersion:     "1.0.0",
		CanonicalImage: "ghcr.io/filippolmt/proximo:v1.0.0",
		CAPath:         "/home/dev/.proximo/tls/ca.pem",
		ResolverPath:   "/etc/resolver/test",
		ResolverRemedy: "resolvectl status",
		CertutilRemedy: "sudo apt-get install -y libnss3-tools",
		FileExists:     func(string) bool { return true },
		PortHeldBy: func(context.Context, int, string) (PortHolder, string) {
			return PortStack, "proximo-traefik-1"
		},
		QueryLocal:          func(context.Context, string) (string, error) { return "127.0.0.1", nil },
		SystemResolve:       func(context.Context, string) (string, error) { return "127.0.0.1", nil },
		SystemTrusted:       func(context.Context) (bool, error) { return true, nil },
		CertutilInstallable: func() bool { return true },
		NSSTrusted:          func(context.Context) (int, int, error) { return 2, 2, nil },
		Docker:              func(context.Context) error { return nil },
		Stack: func(context.Context) (docker.StackInfo, error) {
			return docker.StackInfo{
				Running: true,
				Version: "1.0.0",
				Image:   "ghcr.io/filippolmt/proximo:v1.0.0",
				Roles:   []string{"traefik", "dns", "watcher", "inspector"},
			}, nil
		},
		Routes: func(context.Context) ([]docker.Route, error) {
			return []docker.Route{{Container: "web", Host: "web.test"}}, nil
		},
		AgentSkill: func() ([]skill.Copy, error) {
			return []skill.Copy{{Dest: skill.Dest{Dir: "/home/dev/.claude/skills/proximo"}, State: skill.Current}}, nil
		},
	}
}

// brokenEnv is the opposite machine: every reading gives the worst answer it
// has, so each check can be driven to its failure independently.
func brokenEnv() Env {
	env := healthyEnv()
	env.FileExists = func(string) bool { return false }
	env.PortHeldBy = func(context.Context, int, string) (PortHolder, string) { return PortProcess, "" }
	env.QueryLocal = func(context.Context, string) (string, error) { return "", errors.New("no answer") }
	env.SystemResolve = func(context.Context, string) (string, error) { return "", nil }
	env.SystemTrusted = func(context.Context) (bool, error) { return false, nil }
	env.CertutilInstallable = func() bool { return false }
	env.NSSTrusted = func(context.Context) (int, int, error) { return 0, 3, nil }
	env.Docker = func(context.Context) error { return errors.New("daemon down") }
	env.Stack = func(context.Context) (docker.StackInfo, error) { return docker.StackInfo{}, nil }
	env.Routes = func(context.Context) ([]docker.Route, error) {
		return []docker.Route{{Container: "web", Note: "api.test is served by shop-api-1"}}, nil
	}
	return env
}

func TestHealthyMachineFailsNothing(t *testing.T) {
	rep := Run(context.Background(), All(healthyEnv()))
	for _, o := range rep.Outcomes {
		if o.Result.Status != Pass {
			t.Errorf("%s = %s (%s), want pass", o.Check.ID, o.Result.Status, o.Result.Detail)
		}
	}
	if !rep.OK() {
		t.Error("report on a healthy machine is not OK")
	}
}

// Every failure carries a Remedy: the term promises a command, and a failure
// without one turns Remedy into a synonym for advice. Each check is run
// directly so prerequisite skipping cannot hide one.
func TestEveryFailureCarriesARemedy(t *testing.T) {
	for _, c := range All(brokenEnv()) {
		res := c.Run(context.Background())
		if res.Status != Fail {
			continue
		}
		if res.Remedy == "" {
			t.Errorf("%s failed without a remedy", c.ID)
		}
		if res.Detail == "" {
			t.Errorf("%s failed without saying what it observed", c.ID)
		}
	}
}

func TestEveryCheckHasAnIDNameAndDoc(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range All(healthyEnv()) {
		switch {
		case c.ID == "":
			t.Errorf("check %q has no ID", c.Name)
		case c.Name == "":
			t.Errorf("check %q has no name", c.ID)
		case c.Doc == "":
			t.Errorf("check %q names no troubleshooting section", c.ID)
		case seen[c.ID]:
			t.Errorf("duplicate check ID %q", c.ID)
		}
		// Checked against what has run *so far*, not against the whole list: a
		// prerequisite that comes later has no status yet when the dependent
		// runs, so it silently gates nothing. That is the one way the skip
		// contract fails quietly, and it must fail loudly here instead.
		for _, need := range c.Needs {
			if !seen[need] {
				t.Errorf("check %q waits on %q, which does not run before it", c.ID, need)
			}
		}
		seen[c.ID] = true
	}
}

// A prerequisite that did not pass skips what depends on it, naming what it
// waited on: an unreachable Docker daemon is one cause, not a dozen red lines.
func TestFailedPrerequisiteSkipsDependents(t *testing.T) {
	rep := Run(context.Background(), []Check{
		{ID: "a", Name: "A holds", Run: func(context.Context) Result { return Failed("remedy a", "broken") }},
		{ID: "b", Name: "B holds", Needs: []string{"a"}, Run: func(context.Context) Result {
			t.Error("b ran although its prerequisite failed")
			return Passed("")
		}},
	})
	b := rep.Outcomes[1].Result
	if b.Status != Skip {
		t.Fatalf("b = %s, want skip", b.Status)
	}
	if !strings.Contains(b.Detail, "A holds") {
		t.Errorf("skip detail %q does not name what it waited on", b.Detail)
	}
	if len(rep.Failures()) != 1 {
		t.Errorf("failures = %d, want 1 (a skip is not a failure)", len(rep.Failures()))
	}
}

func TestSkipIsNotAFailure(t *testing.T) {
	rep := Run(context.Background(), []Check{
		{ID: "a", Name: "A holds", Run: func(context.Context) Result { return Skipped("nothing to answer with") }},
	})
	if !rep.OK() {
		t.Error("a report whose only outcome is a skip is not OK")
	}
}

// The two DNS checks are one answer: a corporate VPN produces exactly this
// pair, and neither check alone gives it.
func TestVPNPairServerAnswersHostDoesNotUseIt(t *testing.T) {
	env := healthyEnv()
	env.SystemResolve = func(context.Context, string) (string, error) { return "10.0.0.1", nil }

	rep := Run(context.Background(), All(env))
	got := map[string]Result{}
	for _, o := range rep.Outcomes {
		got[o.Check.ID] = o.Result
	}
	if got[IDDNSServer].Status != Pass {
		t.Errorf("dns-server = %s, want pass", got[IDDNSServer].Status)
	}
	if got[IDDNSResolver].Status != Fail {
		t.Fatalf("dns-resolver = %s, want fail", got[IDDNSResolver].Status)
	}
	if got[IDDNSResolver].Remedy != env.ResolverRemedy {
		t.Errorf("remedy = %q, want the resolver question %q", got[IDDNSResolver].Remedy, env.ResolverRemedy)
	}
}

// A machine that was never installed answers with one failure and a stack of
// skips, not a wall of red.
func TestNeverInstalledGatesTheHostChecks(t *testing.T) {
	env := healthyEnv()
	env.FileExists = func(string) bool { return false }

	rep := Run(context.Background(), All(env))
	for _, o := range rep.Outcomes {
		switch o.Check.ID {
		case IDInstalled:
			if o.Result.Status != Fail || o.Result.Remedy != "proximo install" {
				t.Errorf("installed = %s remedy %q, want fail with `proximo install`", o.Result.Status, o.Result.Remedy)
			}
		case IDTrustSystem, IDTrustNSS, IDDNSResolver:
			if o.Result.Status != Skip {
				t.Errorf("%s = %s, want skip behind the install gate", o.Check.ID, o.Result.Status)
			}
		case IDDocker, IDPortHTTP, IDPortHTTPS, IDPortDNS:
			// The pre-install checks still run: a developer must learn that
			// Docker is missing before running the install that needs it.
			if o.Result.Status != Pass {
				t.Errorf("%s = %s, want the pre-install check to still run", o.Check.ID, o.Result.Status)
			}
		}
	}
}

func TestPortHolderPicksTheRemedy(t *testing.T) {
	cases := []struct {
		name   string
		holder PortHolder
		who    string
		status Status
		remedy string
	}{
		{"free", PortFree, "", Pass, ""},
		{"held by the stack", PortStack, "proximo-traefik-1", Pass, ""},
		{"held by another container", PortContainer, "nginx", Fail, "docker ps --filter publish=443"},
		{"held by a host process", PortProcess, "", Fail, "sudo lsof -nP -iTCP:443 -sTCP:LISTEN"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := healthyEnv()
			env.PortHeldBy = func(context.Context, int, string) (PortHolder, string) { return tc.holder, tc.who }
			res := portCheck(env, IDPortHTTPS, 443, "tcp", "port-443-or-80-already-in-use").Run(context.Background())
			if res.Status != tc.status {
				t.Errorf("status = %s, want %s", res.Status, tc.status)
			}
			if res.Remedy != tc.remedy {
				t.Errorf("remedy = %q, want %q", res.Remedy, tc.remedy)
			}
		})
	}
}

func TestDNSPortRemedyAsksAboutUDP(t *testing.T) {
	env := healthyEnv()
	env.PortHeldBy = func(context.Context, int, string) (PortHolder, string) { return PortProcess, "" }
	res := portCheck(env, IDPortDNS, 5354, "udp", "dns-port-already-in-use").Run(context.Background())
	if want := "sudo lsof -nP -iUDP:5354"; res.Remedy != want {
		t.Errorf("remedy = %q, want %q", res.Remedy, want)
	}
}

// A degraded stack — traefik up, watcher gone — is a failure, not a pass:
// without the watcher routes stop updating, which is the documented symptom.
func TestDegradedStackFails(t *testing.T) {
	env := healthyEnv()
	env.Stack = func(context.Context) (docker.StackInfo, error) {
		return docker.StackInfo{Running: true, Version: "1.0.0", Roles: []string{"traefik", "dns"}}, nil
	}
	res := checkByID(t, All(env), IDStack).Run(context.Background())
	if res.Status != Fail || !strings.Contains(res.Detail, "watcher") {
		t.Errorf("stack = %s (%s), want a failure naming the watcher", res.Status, res.Detail)
	}
}

// A container still coming up, and a route whose inspection was refused, are
// both being served — or about to be. Neither may turn `doctor` red: a
// report that goes red during every restart is one nobody trusts.
func TestTransientAndInspectNotesAreNotRouteFailures(t *testing.T) {
	env := healthyEnv()
	env.Routes = func(context.Context) ([]docker.Route, error) {
		return []docker.Route{
			{Container: "booting", Host: "web.test", Note: docker.NoteStarting},
			{Container: "balanced", Host: "app.test", URL: "https://app.test",
				InspectNote: "inspection off: route balances across replicas"},
		}, nil
	}
	res := checkByID(t, All(env), IDRoutes).Run(context.Background())
	if res.Status != Pass {
		t.Errorf("routes = %s (%s), want pass", res.Status, res.Detail)
	}
}

func TestUnhealthyRouteIsARouteFailure(t *testing.T) {
	env := healthyEnv()
	env.Routes = func(context.Context) ([]docker.Route, error) {
		return []docker.Route{
			{Container: "web", Host: "web.test", Note: docker.NoteUnhealthy},
		}, nil
	}
	res := checkByID(t, All(env), IDRoutes).Run(context.Background())
	if res.Status != Fail {
		t.Errorf("routes = %s, want fail", res.Status)
	}
}

func TestUnservedRouteFailsAndNamesTheContainerToInspect(t *testing.T) {
	env := healthyEnv()
	env.Routes = func(context.Context) ([]docker.Route, error) {
		return []docker.Route{
			{Container: "web", Host: "web.test"},
			{Container: "work-api-1", Note: "api.test is served by shop-api-1"},
		}, nil
	}
	res := checkByID(t, All(env), IDRoutes).Run(context.Background())
	if res.Status != Fail {
		t.Fatalf("routes = %s, want fail", res.Status)
	}
	if !strings.Contains(res.Detail, "work-api-1: api.test is served by shop-api-1") {
		t.Errorf("detail %q does not carry the route note", res.Detail)
	}
	if !strings.Contains(res.Remedy, "work-api-1") || strings.Contains(res.Remedy, "web") {
		t.Errorf("remedy %q should inspect only the unserved container", res.Remedy)
	}
}

// A contested host and a mislabelled container are two different failures, so
// the report must not send a developer to the wrong section — nor offer a
// remedy that cannot cure the one they have.
func TestCollisionIsExplainedApartFromAMislabelledContainer(t *testing.T) {
	env := healthyEnv()
	env.Routes = func(context.Context) ([]docker.Route, error) {
		return []docker.Route{
			{Container: "work-api-1", Host: "api.test", Collision: true,
				Note: "api.test is served by shop-api-1; this container answers at api.work.test"},
		}, nil
	}
	res := checkByID(t, All(env), IDRoutes).Run(context.Background())
	if res.Doc != "a-host-collision-is-reported" {
		t.Errorf("doc = %q, want the collision section", res.Doc)
	}
	if strings.Contains(res.Remedy, "docker inspect") {
		t.Errorf("remedy = %q: inspecting labels does not cure a contested host", res.Remedy)
	}
}

func TestStackImageOverrideIsADoctorFailure(t *testing.T) {
	env := healthyEnv()
	env.Stack = func(context.Context) (docker.StackInfo, error) {
		return docker.StackInfo{Running: true, Version: "1.0.0", Image: "proximo:src",
			Roles: []string{"traefik", "dns", "watcher"}}, nil
	}
	res := checkByID(t, All(env), IDStackImage).Run(context.Background())
	if res.Status != Fail || res.Remedy != "proximo up" {
		t.Errorf("stack-image = %s remedy %q, want fail with `proximo up`", res.Status, res.Remedy)
	}
}

func TestVersionSkewIsADoctorFailure(t *testing.T) {
	env := healthyEnv()
	env.Stack = func(context.Context) (docker.StackInfo, error) {
		return docker.StackInfo{Running: true, Version: "0.9.0", Image: env.CanonicalImage,
			Roles: []string{"traefik", "dns", "watcher"}}, nil
	}
	res := checkByID(t, All(env), IDStackVersion).Run(context.Background())
	if res.Status != Fail || res.Remedy != "proximo update" {
		t.Errorf("stack-version = %s remedy %q, want fail with `proximo update`", res.Status, res.Remedy)
	}
}

// The stack is read once per pass however many checks ask about it.
func TestStackIsReadOncePerPass(t *testing.T) {
	env := healthyEnv()
	calls := 0
	env.Stack = func(context.Context) (docker.StackInfo, error) {
		calls++
		return docker.StackInfo{Running: true, Version: "1.0.0", Image: env.CanonicalImage,
			Roles: []string{"traefik", "dns", "watcher"}}, nil
	}
	Run(context.Background(), All(env))
	if calls != 1 {
		t.Errorf("stack read %d times, want 1", calls)
	}
}

func TestPreflightIsTheSubsetThatNeedsNoInstall(t *testing.T) {
	var ids []string
	for _, c := range Preflight(healthyEnv()) {
		ids = append(ids, c.ID)
	}
	want := []string{IDDocker, IDPortHTTP, IDPortHTTPS, IDPortDNS}
	if strings.Join(ids, ",") != strings.Join(want, ",") {
		t.Errorf("preflight = %v, want %v", ids, want)
	}
	// `up` changes no host configuration, so it must not be gated on whether
	// browser trust could be installed.
	for _, c := range Preflight(healthyEnv()) {
		if c.ID == IDCertutil {
			t.Error("up is gated on a check about a store it never writes")
		}
	}
}

// The registry is one list, and the two subsets are prefixes of it: Preflight ⊂
// PreInstall ⊂ All, so no subset can drift away from the whole.
func TestSubsetsArePrefixesOfTheRegistry(t *testing.T) {
	prefixOf := func(t *testing.T, name string, subset, whole []Check) {
		t.Helper()
		if len(subset) > len(whole) {
			t.Fatalf("%s is longer than what contains it", name)
		}
		for i, c := range subset {
			if whole[i].ID != c.ID {
				t.Errorf("%s[%d] = %s, but the containing list has %s", name, i, c.ID, whole[i].ID)
			}
		}
	}
	env := healthyEnv()
	prefixOf(t, "Preflight", Preflight(env), PreInstall(env))
	prefixOf(t, "PreInstall", PreInstall(env), All(env))
}

// install writes the NSS store, so it learns before it touches anything that
// the store can be written at all — the guarantee that would otherwise be paid
// for with a sudo prompt and a rollback.
func TestInstallIsGatedOnBrowserTrustBeingInstallable(t *testing.T) {
	env := healthyEnv()
	env.CertutilInstallable = func() bool { return false }

	rep := Run(context.Background(), PreInstall(env))
	failures := rep.Failures()
	if len(failures) != 1 || failures[0].Check.ID != IDCertutil {
		t.Fatalf("failures = %v, want just the certutil check", failures)
	}
	if failures[0].Result.Remedy != env.CertutilRemedy {
		t.Errorf("remedy = %q, want %q", failures[0].Result.Remedy, env.CertutilRemedy)
	}
}

func checkByID(t *testing.T, list []Check, id string) Check {
	t.Helper()
	for _, c := range list {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("no check %q in the registry", id)
	return Check{}
}

// A check reads the host and never writes it. Resolving where the CA would live
// must not create the state home: the machine that was never installed is
// exactly the one running `proximo doctor`.
func TestDefaultEnvWritesNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if _, err := DefaultEnv("test"); err != nil {
		t.Fatalf("DefaultEnv: %v", err)
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatalf("read home: %v", err)
	}
	for _, e := range entries {
		t.Errorf("DefaultEnv created %s", filepath.Join(home, e.Name()))
	}
}

// The TCP question connects rather than binds. Binding :80 or :443 as the
// unprivileged user proximo runs as fails with EACCES on a free port, which
// would report a stranger holding it and refuse to install on a healthy
// machine.
func TestHeldSeesAListenerAndOnlyAListener(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if !held(port, "tcp") {
		t.Error("a listening TCP port was reported as free")
	}
	if err := l.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if held(port, "tcp") {
		t.Error("a TCP port with nothing on it was reported as held")
	}

	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	uport := pc.LocalAddr().(*net.UDPAddr).Port
	if !held(uport, "udp") {
		t.Error("a bound UDP port was reported as free")
	}
	if err := pc.Close(); err != nil {
		t.Fatalf("close udp: %v", err)
	}
	if held(uport, "udp") {
		t.Error("a UDP port with nothing on it was reported as held")
	}
}

// The privileged ports proximo cares about must not answer "held" merely
// because an unprivileged process cannot bind them.
func TestPrivilegedPortsAreNotHeldByDefault(t *testing.T) {
	for _, port := range []int{80, 443} {
		if held(port, "tcp") {
			t.Skipf("something is listening on :%d in this environment", port)
		}
	}
}

// The agent-skill Check reports what auto-update could not reach, and says
// nothing at all on a host that installed no skill.
func TestAgentSkillCheck(t *testing.T) {
	at := func(state skill.State, scope skill.Scope, dir string) skill.Copy {
		return skill.Copy{Dest: skill.Dest{Scope: scope, Dir: dir}, State: state}
	}
	for _, tc := range []struct {
		name   string
		copies []skill.Copy
		want   Status
		remedy string
		// detail is a substring the observation must carry, where the point of
		// the case is what it says rather than only its status.
		detail string
	}{
		{
			name: "no managed copy is skipped, not failed",
			copies: []skill.Copy{
				at(skill.Absent, skill.Global, "/home/dev/.claude/skills/proximo"),
				at(skill.Absent, skill.Global, "/home/dev/.codex/skills/proximo"),
			},
			want: Skip,
		},
		{
			// The skip says a copy is there and proximo did not put it there,
			// rather than reading as "no agent skill" to someone looking at it.
			name:   "an unmanaged copy is named by the skip",
			copies: []skill.Copy{at(skill.Unmanaged, skill.Global, "/home/dev/.claude/skills/proximo")},
			want:   Skip,
			detail: "another channel",
		},
		{
			name: "an unmanaged copy is named beside a passing one",
			copies: []skill.Copy{
				at(skill.Current, skill.Project, "/repo/.claude/skills/proximo"),
				at(skill.Unmanaged, skill.Global, "/home/dev/.codex/skills/proximo"),
			},
			want:   Pass,
			detail: "unmanaged",
		},
		{
			name:   "a copy behind the binary is cured by a plain install",
			copies: []skill.Copy{at(skill.Stale, skill.Project, "/repo/.claude/skills/proximo")},
			want:   Fail,
			remedy: "proximo skill install",
		},
		{
			// `skill install` defaults to project scope, so a failure that is
			// only about the home copy must name the scope that reaches it.
			name:   "a global copy names the scope that reaches it",
			copies: []skill.Copy{at(skill.Stale, skill.Global, "/home/dev/.claude/skills/proximo")},
			want:   Fail,
			remedy: "proximo skill install --scope global",
		},
		{
			// Distinct remedy: nothing but --force overwrites an edited copy.
			name: "an edited copy names the only command that overwrites it",
			copies: []skill.Copy{
				at(skill.Stale, skill.Project, "/a"),
				at(skill.Modified, skill.Project, "/b"),
			},
			want:   Fail,
			remedy: "proximo skill install --force",
		},
		{
			name:   "a copy level with the binary passes",
			copies: []skill.Copy{at(skill.Current, skill.Global, "/home/dev/.claude/skills/proximo")},
			want:   Pass,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := healthyEnv()
			env.AgentSkill = func() ([]skill.Copy, error) { return tc.copies, nil }
			res := agentSkill(env)
			if res.Status != tc.want {
				t.Fatalf("status %q (%s), want %q", res.Status, res.Detail, tc.want)
			}
			if res.Remedy != tc.remedy {
				t.Errorf("remedy %q, want %q", res.Remedy, tc.remedy)
			}
			if tc.detail != "" && !strings.Contains(res.Detail, tc.detail) {
				t.Errorf("detail %q does not mention %q", res.Detail, tc.detail)
			}
		})
	}
}
