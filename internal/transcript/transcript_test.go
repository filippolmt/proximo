package transcript

import (
	"fmt"
	"strings"
	"testing"
)

// numbered builds n lines whose content names their own position, so what
// survived a cut can be checked against an independent expectation rather than
// against the cutter's own arithmetic.
func numbered(n int) []byte {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	return []byte(b.String())
}

func TestCutKeepsEverythingUnderTheLimit(t *testing.T) {
	raw := numbered(5)
	got := cut(raw, 1<<20)
	if got.Dropped != 0 {
		t.Errorf("Dropped = %d, want 0", got.Dropped)
	}
	if len(got.Tail) != 0 {
		t.Errorf("nothing was elided, so there is no separate tail: %v", got.Tail)
	}
	if len(got.Head) != 5 || got.Head[0] != "line 1" || got.Head[4] != "line 5" {
		t.Errorf("Head = %v, want the 5 lines in order", got.Head)
	}
}

// The head carries a panic's message, the tail its most recent lines, and how
// many lines went missing in between is stated rather than left to be noticed.
func TestCutKeepsHeadAndTailAndDeclaresTheElision(t *testing.T) {
	raw := numbered(1000) // "line N\n" — 8 to 10 bytes each
	got := cut(raw, 200)

	if len(got.Head) == 0 || len(got.Tail) == 0 {
		t.Fatalf("a cut Transcript must keep both ends: head %d, tail %d", len(got.Head), len(got.Tail))
	}
	if got.Head[0] != "line 1" {
		t.Errorf("Head starts at %q, want the first line the container wrote", got.Head[0])
	}
	if last := got.Tail[len(got.Tail)-1]; last != "line 1000" {
		t.Errorf("Tail ends at %q, want the most recent line", last)
	}
	if want := 1000 - len(got.Head) - len(got.Tail); got.Dropped != want {
		t.Errorf("Dropped = %d, but %d lines are unaccounted for", got.Dropped, want)
	}
	if got.Dropped == 0 {
		t.Error("1000 lines were cut to 200 bytes and nothing was declared elided")
	}
	if n := size(got); n > 200 {
		t.Errorf("cut to %d bytes, over the 200-byte limit", n)
	}
}

// A budget too small for even one line still quotes both: the Exchange being
// asked about is the one that matters, and a Transcript that silently came back
// empty is the failure this refuses. Nothing was elided, so nothing is declared.
func TestCutNeverReturnsNothing(t *testing.T) {
	raw := []byte(strings.Repeat("x", 500) + "\n" + strings.Repeat("y", 500) + "\n")
	got := cut(raw, 10)
	if got.Dropped != 0 {
		t.Errorf("Dropped = %d, but both lines were kept", got.Dropped)
	}
	if len(got.Head) != 2 || len(got.Tail) != 0 {
		t.Fatalf("head %d, tail %d — nothing was dropped, so it is all one quote", len(got.Head), len(got.Tail))
	}
}

func TestCutOfNothingIsEmpty(t *testing.T) {
	for _, raw := range [][]byte{nil, {}, []byte("\n\n  \n")} {
		if got := cut(raw, 100); len(got.Head) != 0 || len(got.Tail) != 0 || got.Dropped != 0 {
			t.Errorf("cut(%q) = %+v, want an empty Transcript", raw, got)
		}
	}
}

func size(tr Transcript) int {
	n := 0
	for _, l := range append(append([]string{}, tr.Head...), tr.Tail...) {
		n += len(l) + 1
	}
	return n
}
