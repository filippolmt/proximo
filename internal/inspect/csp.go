package inspect

import (
	"net/http"
	"strings"
)

// Inspection has to work on any stack, and plenty of projects serve a
// Content-Security-Policy in development. An enforced policy silently defeats
// Inspection in two different ways — it can refuse to load the injected script,
// and it can refuse to let that script POST its reports — so the hop reconciles
// both before either fails invisibly.
//
// For loading, the order of preference is: reuse the page's own nonce, do nothing
// when the policy already admits a same-origin script, and only then relax and
// say so. Relaxing is done by adding a nonce rather than a source, because a
// nonce is the one thing that works under 'strict-dynamic' and under a hash-only
// policy alike. For reporting, the tunnel is same-origin, so `'self'` is both
// necessary and sufficient — a nonce means nothing to connect-src.

// scriptKinds are the directives that can govern loading a `<script src>`, most
// specific first: script-src-elem wins over script-src, which wins over
// default-src. connectKinds are the ones that govern the report POST.
var (
	scriptKinds  = []string{"script-src-elem", "script-src", "default-src"}
	connectKinds = []string{"connect-src", "default-src"}
)

// policy is one parsed Content-Security-Policy header value: its directives, in
// order. Keeping it a type rather than a bare string is what lets the three
// questions the hop asks — does it carry a nonce, does it already admit us, and
// how do I widen it — share one notion of which directive is in charge.
type policy struct{ directives []string }

func parsePolicy(v string) policy {
	var out []string
	for _, d := range strings.Split(v, ";") {
		if d = strings.TrimSpace(d); d != "" {
			out = append(out, d)
		}
	}
	return policy{out}
}

func (p policy) String() string { return strings.Join(p.directives, "; ") }

// governing returns the index of the directive that decides the question kinds
// asks about, or -1 when the policy does not constrain it at all.
func (p policy) governing(kinds []string) int {
	for _, want := range kinds {
		for i, d := range p.directives {
			f := strings.Fields(d)
			if len(f) > 0 && strings.EqualFold(f[0], want) {
				return i
			}
		}
	}
	return -1
}

// sources lists the source expressions of the governing directive, unquoted.
func (p policy) sources(kinds []string) []string {
	i := p.governing(kinds)
	if i < 0 {
		return nil
	}
	var out []string
	for _, tok := range strings.Fields(p.directives[i])[1:] {
		out = append(out, strings.Trim(tok, "'"))
	}
	return out
}

// nonce returns the nonce the page already uses for its scripts, if any.
func (p policy) nonce() string {
	for _, src := range p.sources(scriptKinds) {
		if v, ok := strings.CutPrefix(src, "nonce-"); ok {
			return v
		}
	}
	return ""
}

// admitsSelf reports whether the governing directive already allows the page's
// own origin. 'strict-dynamic' voids source expressions for scripts, so a policy
// carrying it never qualifies however permissive it otherwise looks.
func (p policy) admitsSelf(kinds []string) bool {
	i := p.governing(kinds)
	if i < 0 {
		return true // not constrained at all
	}
	srcs := p.sources(kinds)
	for _, s := range srcs {
		if s == "strict-dynamic" {
			return false
		}
	}
	for _, s := range srcs {
		if s == "self" {
			return true
		}
	}
	return false
}

// widen appends a source to whichever directive governs kinds. A policy that does
// not constrain kinds at all is left alone.
func (p *policy) widen(kinds []string, source string) {
	if i := p.governing(kinds); i >= 0 {
		p.directives[i] += " " + source
	}
}

// reconcileCSP makes the response admit the injected agent and its reports. It
// returns the nonce to put on the tag (empty when none is needed) and a warning
// naming what was relaxed (empty when the policy was left untouched).
func reconcileCSP(h http.Header) (nonce, warning string) {
	vals := h.Values("Content-Security-Policy")
	if len(vals) == 0 {
		return "", ""
	}

	policies := make([]policy, len(vals))
	for i, v := range vals {
		policies[i] = parsePolicy(v)
	}

	// ponytail: a single policy header is the case worth being precise about.
	// Several enforced policies intersect in the browser, so when there are
	// several we widen them all with one minted nonce rather than trying to reuse
	// each one's own.
	needScript, needConnect := false, false
	for _, p := range policies {
		if !p.admitsSelf(scriptKinds) {
			needScript = true
		}
		if !p.admitsSelf(connectKinds) {
			needConnect = true
		}
	}
	// The page's own nonce, when it has one, already admits the injected tag —
	// nothing to relax on the script side, and nothing to warn about.
	if len(vals) == 1 {
		if n := policies[0].nonce(); n != "" {
			nonce, needScript = n, false
		}
	}
	if !needScript && !needConnect {
		return nonce, ""
	}
	if needScript {
		nonce = NewID()
	}
	for i := range policies {
		if needScript {
			policies[i].widen(scriptKinds, "'nonce-"+nonce+"'")
		}
		if needConnect {
			policies[i].widen(connectKinds, "'self'")
		}
	}
	relaxed := make([]string, len(policies))
	for i, p := range policies {
		relaxed[i] = p.String()
	}
	h["Content-Security-Policy"] = relaxed

	switch {
	case needScript && needConnect:
		warning = "relaxed Content-Security-Policy (script-src and connect-src) so the proximo agent could load and report"
	case needScript:
		warning = "relaxed Content-Security-Policy (script-src) so the proximo agent could load"
	default:
		warning = "relaxed Content-Security-Policy (connect-src) so the proximo agent could report"
	}
	return nonce, warning
}
