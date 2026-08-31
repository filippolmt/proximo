// Package transcript reads back what a Project's own container wrote while an
// Exchange was live. proximo collects nothing of its own and stores nothing: a
// Transcript is quoted from the container's existing output, cut to the window
// of the Exchange, at the moment the CLI prints it. See
// docs/adr/0006-the-transcript-is-quoted-never-stored.md.
package transcript

import (
	"bytes"
	"strings"
)

// Transcript is what one container wrote in the window of one Exchange, quoted
// verbatim and never interpreted. It is bounded, and the bound is declared: a
// truncation nobody is told about is the one after which a reader stops looking.
type Transcript struct {
	// Container is the name of the container quoted, and Replicas how many the
	// service has. Without the count an agent reads "happens only sometimes" as a
	// race condition when the cause is one replica running stale config.
	Container string `json:"container"`
	Replicas  int    `json:"replicas,omitempty"`

	// Head is the start of the window, Tail its most recent lines, and Dropped
	// how many lines were elided in between. A panic's message is at the head and
	// its most recent output at the tail, so neither end may be the one cut.
	// Dropped zero means Head is the whole thing and Tail is empty.
	Head    []string `json:"head,omitempty"`
	Tail    []string `json:"tail,omitempty"`
	Dropped int      `json:"dropped,omitempty"`

	// Silence says why there is nothing to quote, and is set only when there is
	// nothing. A silence without a named cause is what makes a working tool look
	// broken.
	Silence string `json:"silence,omitempty"`

	// Overlap says another Exchange of this container was live in the same
	// window. The cut is temporal, so their lines interleave and nothing here can
	// tell them apart. proximo reports the overlap rather than attributing a
	// line — quoting the wrong container's stack trace to something about to edit
	// code is the failure this refuses.
	Overlap int `json:"overlap,omitempty"`
}

// Empty reports whether there is nothing to quote.
func (t Transcript) Empty() bool { return len(t.Head) == 0 && len(t.Tail) == 0 }

// DefaultLimit is how many bytes of a container's output one Transcript quotes
// when it is rendered inline beside an Exchange. `proximo errors transcript` asks
// for far more: it is the command for reading the whole thing.
const DefaultLimit = 4 << 10

// cut turns raw container output into a bounded Transcript, keeping both ends
// and declaring what it dropped in between.
//
// ponytail: whole lines only, so one pathological line — a 100 kB JSON blob —
// puts the result over limit rather than being cut mid-token. Both ends are
// always kept for the same reason the Store never evicts its most recent
// Exchange: the thing being asked about is the thing that matters. Cut inside a
// line only if such blobs turn out to be common, and declare that elision too.
func cut(raw []byte, limit int) Transcript {
	lines := splitLines(raw)
	if len(lines) == 0 {
		return Transcript{}
	}

	total := 0
	for _, l := range lines {
		total += len(l) + 1
	}
	if total <= limit {
		return Transcript{Head: lines}
	}

	// Half the budget to each end, and at least one line to each regardless.
	half := limit / 2
	head := take(lines, half)
	tail := takeBack(lines[len(head):], half)

	dropped := len(lines) - len(head) - len(tail)
	if dropped <= 0 {
		// Everything fit after all, once whole lines were counted.
		return Transcript{Head: lines}
	}
	return Transcript{Head: head, Tail: tail, Dropped: dropped}
}

// take returns the longest prefix of lines fitting in budget, never fewer than
// one line and never every line — the tail must keep something to quote.
func take(lines []string, budget int) []string {
	n, used := 0, 0
	for n < len(lines)-1 {
		next := used + len(lines[n]) + 1
		if n > 0 && next > budget {
			break
		}
		used, n = next, n+1
	}
	return lines[:n]
}

// takeBack returns the longest suffix of lines fitting in budget, never fewer
// than one line.
func takeBack(lines []string, budget int) []string {
	n, used := 0, 0
	for n < len(lines) {
		next := used + len(lines[len(lines)-1-n]) + 1
		if n > 0 && next > budget {
			break
		}
		used, n = next, n+1
	}
	return lines[len(lines)-n:]
}

// splitLines drops the blank lines a container's output ends with, and the
// carriage returns a Windows-built image leaves behind. Everything else is
// quoted exactly as it was written.
func splitLines(raw []byte) []string {
	var out []string
	for _, l := range bytes.Split(raw, []byte("\n")) {
		s := strings.TrimRight(string(l), "\r")
		if strings.TrimSpace(s) == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}
