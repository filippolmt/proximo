// Package checks verifies the environment proximo needs, one independently
// verifiable statement at a time, and collects the outcomes into one Report.
//
// A Check reports; it never repairs, and it never elevates: everything proximo
// must read is readable unprivileged, and a check that needed a password would
// be describing a repair. Every failure therefore carries a Remedy — the cure
// where one exists, and otherwise the command whose own output names the cause.
package checks

import (
	"context"
	"fmt"
	"time"
)

// Status is the outcome of one Check. There are exactly three. A fourth
// ("warn") would mean a failure proximo decided not to insist on, and that
// decision belongs to whoever reads the Report, not to proximo writing it.
type Status string

const (
	// Pass is a statement that held.
	Pass Status = "pass"
	// Fail is a statement that did not hold. It always carries a Remedy.
	Fail Status = "fail"
	// Skip is a statement the environment could not answer — never one whose
	// answer was inconvenient. A skip names what it waited on.
	Skip Status = "skip"
)

// Result is what one Check observed.
type Result struct {
	Status Status
	// Detail is what was observed, in the developer's terms. A passing check
	// says it too: a pass narrows the search as much as a failure does.
	Detail string
	// Remedy is the exact command the developer runs next. Required on Fail,
	// and meaningless otherwise.
	Remedy string
	// Doc names a troubleshooting section for this failure when it is not the
	// one the Check usually points at. One check can fail for causes that are
	// documented apart — a contested host is not a mislabelled container — and
	// sending a developer to the wrong section is worse than sending them to
	// none. Empty means the Check's own section.
	Doc string
}

// Passed records a statement that held, with what was observed.
func Passed(format string, a ...any) Result {
	return Result{Status: Pass, Detail: fmt.Sprintf(format, a...)}
}

// Failed records a statement that did not hold, with the command that clears
// it (or, where nothing cures it, the command whose output names the cause).
func Failed(remedy, format string, a ...any) Result {
	return Result{Status: Fail, Detail: fmt.Sprintf(format, a...), Remedy: remedy}
}

// Explains points a failure at a troubleshooting section other than its
// Check's own.
func (r Result) Explains(doc string) Result {
	r.Doc = doc
	return r
}

// Skipped records a statement the environment could not answer, naming what it
// waited on.
func Skipped(format string, a ...any) Result {
	return Result{Status: Skip, Detail: fmt.Sprintf(format, a...)}
}

// Check is one independently verifiable statement about the environment.
type Check struct {
	// ID is how other checks name this one as a prerequisite.
	ID string
	// Name is the statement itself, asserted in the positive, so that a pass
	// and a failure read as the same sentence with opposite marks.
	Name string
	// Doc is the anchor of the docs/troubleshooting.md section explaining this
	// failure. A test asserts the section exists: a documented failure with no
	// check, or a check with no explanation, is the drift this prevents.
	Doc string
	// Needs are the IDs of checks that must pass first. A prerequisite that
	// failed or was skipped skips this one instead of producing a second red
	// line for one cause.
	Needs []string
	// Run performs the verification. It must not mutate the host.
	Run func(ctx context.Context) Result
}

// Outcome is one Check and what it observed.
type Outcome struct {
	Check  Check
	Result Result
}

// Report is one complete pass of Checks — passes, failures and skips together,
// which is what makes it a whole thing rather than a stream of errors.
type Report struct {
	Outcomes []Outcome
}

// Run performs each check in order, skipping any whose prerequisite did not
// pass and naming the prerequisite it waited on.
func Run(ctx context.Context, list []Check) Report {
	names := make(map[string]string, len(list))
	status := make(map[string]Status, len(list))
	for _, c := range list {
		names[c.ID] = c.Name
	}

	var rep Report
	for _, c := range list {
		res, blocked := blockedBy(c, names, status)
		if !blocked {
			res = runBounded(ctx, c)
		}
		status[c.ID] = res.Status
		rep.Outcomes = append(rep.Outcomes, Outcome{Check: c, Result: res})
	}
	return rep
}

// checkTimeout bounds one check. A diagnostic tool that hangs is worse than one
// that is wrong, and the machine `proximo doctor` exists for is precisely the
// one where a keychain, a resolver or a Docker socket never answers.
const checkTimeout = 15 * time.Second

// runBounded runs one check under the deadline. The check sees the deadline on
// its own context, so it fails with its own words and its own Remedy rather
// than with a generic timeout: everything that can block takes a context.
func runBounded(ctx context.Context, c Check) Result {
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	defer cancel()
	return c.Run(ctx)
}

// blockedBy returns the skip a check inherits from the first prerequisite that
// did not pass, and whether there was one.
func blockedBy(c Check, names map[string]string, status map[string]Status) (Result, bool) {
	for _, need := range c.Needs {
		if s, ok := status[need]; ok && s != Pass {
			return Skipped("waiting on: %s", names[need]), true
		}
	}
	return Result{}, false
}

// Failures returns the outcomes that did not hold, in report order.
func (r Report) Failures() []Outcome {
	var failed []Outcome
	for _, o := range r.Outcomes {
		if o.Result.Status == Fail {
			failed = append(failed, o)
		}
	}
	return failed
}

// OK reports whether nothing failed. Skips are not failures: an unanswerable
// statement is not a broken one.
func (r Report) OK() bool { return len(r.Failures()) == 0 }
