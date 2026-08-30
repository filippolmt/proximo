package inspect

import (
	"net/http"
	"strings"
)

// Inspection has to work on any stack, and plenty of projects serve a
// Content-Security-Policy in development. A policy the browser enforces will
// block a script the proxy injected, silently — so the hop reconciles it.
//
// The order of preference is: reuse the page's own nonce, do nothing when the
// policy already admits a same-origin script, and only then relax the policy and
// say so. Relaxing is done by adding a nonce rather than a source, because a
// nonce is the one thing that works under 'strict-dynamic' and under a
// hash-only policy alike.

// scriptDirectives are the directives that can govern a `<script src>`, most
// specific first: script-src-elem wins over script-src, which wins over
// default-src.
var scriptDirectives = []string{"script-src-elem", "script-src", "default-src"}

// reconcileCSP makes the response admit the injected agent. It returns the nonce
// to put on the tag (empty when none is needed) and a warning describing the
// relaxation (empty when the policy was left untouched).
func reconcileCSP(h http.Header) (nonce, warning string) {
	vals := h.Values("Content-Security-Policy")
	if len(vals) == 0 {
		return "", ""
	}

	// ponytail: a single policy header is the case worth being precise about.
	// Several enforced policies intersect in the browser, so we relax them all
	// with one minted nonce rather than trying to reuse each one's own.
	if len(vals) == 1 {
		if n := nonceOf(vals[0]); n != "" {
			return n, ""
		}
		if admitsSameOrigin(vals[0]) {
			return "", ""
		}
	}

	nonce = NewID()
	relaxed := make([]string, len(vals))
	for i, v := range vals {
		relaxed[i] = addNonce(v, nonce)
	}
	h["Content-Security-Policy"] = relaxed
	return nonce, "relaxed Content-Security-Policy on this route so the proximo agent could load"
}

// governing returns the index of the directive that decides whether a
// `<script src>` may load, or -1 when the policy does not constrain scripts.
func governing(directives []string) int {
	for _, want := range scriptDirectives {
		for i, d := range directives {
			if name(d) == want {
				return i
			}
		}
	}
	return -1
}

// nonceOf returns the nonce the page already uses for its scripts, if any.
func nonceOf(policy string) string {
	dirs := split(policy)
	i := governing(dirs)
	if i < 0 {
		return ""
	}
	for _, tok := range strings.Fields(dirs[i])[1:] {
		tok = strings.Trim(tok, "'")
		if v, ok := strings.CutPrefix(tok, "nonce-"); ok {
			return v
		}
	}
	return ""
}

// admitsSameOrigin reports whether the policy already lets a script load from the
// page's own origin. 'strict-dynamic' voids source expressions, so a policy
// carrying it never qualifies however permissive it otherwise looks.
func admitsSameOrigin(policy string) bool {
	dirs := split(policy)
	i := governing(dirs)
	if i < 0 {
		return true // scripts are not constrained at all
	}
	tokens := strings.Fields(dirs[i])[1:]
	for _, tok := range tokens {
		if strings.Trim(tok, "'") == "strict-dynamic" {
			return false
		}
	}
	for _, tok := range tokens {
		if strings.Trim(tok, "'") == "self" {
			return true
		}
	}
	return false
}

// addNonce appends a nonce source to whichever directive governs scripts. A
// policy that does not constrain scripts is returned unchanged.
func addNonce(policy, nonce string) string {
	dirs := split(policy)
	i := governing(dirs)
	if i < 0 {
		return policy
	}
	dirs[i] += " 'nonce-" + nonce + "'"
	return strings.Join(dirs, "; ")
}

func split(policy string) []string {
	var out []string
	for _, d := range strings.Split(policy, ";") {
		if d = strings.TrimSpace(d); d != "" {
			out = append(out, d)
		}
	}
	return out
}

func name(directive string) string {
	f := strings.Fields(directive)
	if len(f) == 0 {
		return ""
	}
	return strings.ToLower(f[0])
}
