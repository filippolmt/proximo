package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/filippolmt/proximo/internal/docker"
	"github.com/filippolmt/proximo/internal/transcript"
)

// What proximo says about an Incident and a Reading: how a --service is resolved
// to a qualified name, how an Incident's row and its quoted window are rendered,
// and what a listing owes when the Incident store could not be read or has just
// been emptied. The listing's own shape is listing.go.
// resolveService turns what a developer typed into --service into the qualified
// Service proximo prints. A bare name is accepted when nothing contests it; a
// contested one has its candidates reported rather than one of them chosen, the
// position proximo already holds on a Collision and on an Overlap.
func resolveService(want string, running []docker.Service, incidents []docker.Incident) (docker.Service, error) {
	known := append([]docker.Service(nil), running...)
	for _, inc := range incidents {
		// An Incident outlives the container that produced it, so a service that
		// exited and was removed is still a name that answers.
		known = append(known, inc.Service)
	}
	match, candidates := docker.MatchService(want, known)
	if match != "" {
		return match, nil
	}
	if len(candidates) > 0 {
		names := make([]string, len(candidates))
		for i, c := range candidates {
			names[i] = c.String()
		}
		return "", fmt.Errorf("--service %s is claimed by more than one project: %s. Ask for the qualified name — it is the one a listing prints, and the one a paste of it will find",
			want, strings.Join(names, ", "))
	}
	return "", fmt.Errorf("nothing proximo can see answers to --service %s. `proximo status` lists what it knows; a container with no route becomes known by carrying the label %s=true",
		want, docker.TranscriptLabel)
}

// quoteIncidents reads back what each listed Incident's container wrote in the
// window that Incident closes, keyed by Incident id — the same map an Exchange's
// Transcript lands in, because a Transcript is one thing however its window was
// fixed.
//
// all is every Incident in the window rather than the listed subset: the left
// edge of a window is the previous Incident of the same service, and one held
// back by --limit still happened.
func quoteIncidents(ctx context.Context, r *transcript.Reader, listed, all []docker.Incident, limit int) map[string]transcript.Transcript {
	out := make(map[string]transcript.Transcript, len(listed))
	for _, inc := range listed {
		out[inc.ID] = r.QuoteIncident(ctx, inc, all, limit)
	}
	return out
}

// writeIncident renders one Incident as a row of the same listing an Exchange is
// rendered into: the same instant and id, then the service and what the runtime
// declared where the method, path and status of a request would be. The request
// columns are not padded out with blanks — a hole in a column is a question, not
// information.
func writeIncident(w io.Writer, inc docker.Incident, tr transcript.Transcript) {
	fmt.Fprintf(w, "%s  %s  %s  %s\n",
		inc.At.Local().Format("15:04:05"), inc.ID, inc.Service, inc.Describe())
	if inc.Container != "" && inc.Container != inc.Service.String() {
		fmt.Fprintf(w, "  container %s\n", inc.Container)
	}
	writeTranscriptLines(w, tr, inc.ID)
	fmt.Fprintln(w)
}

// incidentsNote is what a listing owes when the second source could not be read.
// The reader is an agent that will not run `proximo doctor` to learn a one-word
// answer, so the Remedy is handed over on the spot: an absent Incident and an
// unreachable Incident store look identical, and one of them means a
// restart-looping worker is going unreported.
func incidentsNote(ctx context.Context, err error) string {
	if err == nil {
		return ""
	}
	info, statusErr := docker.StackStatus(ctx)
	return incidentsNoteFor(statusErr == nil && !info.Running)
}

// incidentsNoteFor is the wording, given which of the two causes it is. A stack
// that is down and a stack too old to publish Incidents produce the same silence
// and take different Remedies, so the note names one rather than hedging.
func incidentsNoteFor(stackDown bool) string {
	if stackDown {
		return warnPrefix + "The stack is not running, so no Incident is being recorded: nothing here can say whether a container exited or restarted. Run `proximo up`."
	}
	return warnPrefix + "This stack records no Incident, so nothing here can say whether a container exited, restarted or was OOM-killed — version skew, not an absence of Incidents. Run `proximo update` (`proximo doctor` reports it as a failed Check too)."
}

// watcherRestartNote is what an empty Incident listing owes: the store lives in
// the watcher's memory, so "nothing happened" and "it was all thrown away" are
// two very different answers and look identical from the output. The hop's
// listing already says this for Client reports; the same is true here, and
// `proximo up` is an easy thing to have done by accident.
func watcherRestartNote(listing docker.IncidentListing, empty bool) string {
	if !empty || listing.Started.IsZero() {
		return ""
	}
	if uptime := time.Since(listing.Started); uptime < 10*time.Minute {
		return fmt.Sprintf("%sNo Incident, and the watcher restarted %s ago — the Incidents it held were in memory only. Anything the runtime declared before that is gone.",
			warnPrefix, uptime.Round(time.Second))
	}
	return ""
}

// noReadingNote is what a --service owes when no container of it is running: the
// present tense has no answer, and the absence is named rather than left as a
// gap. A note rather than a line under the readings because an omitted "readings"
// member cannot say which absence it is — without it, a --service with nothing
// running is byte-identical to an invocation that named no service.
func noReadingNote(service docker.Service) string {
	return fmt.Sprintf("No container of %s is running, so there is no reading to take: the present tense has no answer, and whether the runtime declared anything on the way out is whatever the listing shows.", service)
}

// writeReadings prints what proximo can see of a Service's containers right now,
// one Reading per running container, after the listing. An invocation that named
// no service has no readings to print, and a service with nothing running says so
// in a note instead (noReadingNote): the present tense of something that is not
// alive is not a reading of zero.
func writeReadings(w io.Writer, readings []docker.Reading) {
	for _, rd := range readings {
		fmt.Fprintf(w, "What proximo can see of %s right now: %s.\n", rd.Container, rd.Describe())
	}
}

// writeRefusal declines the conclusion the readings invite. The caller prints it
// only under an empty listing — the misreading it exists to stop is silence read
// as *all fine*, and a screen full of Incidents has no silence in it.
//
// It is the whole of what can honestly be said about a live container that is not
// progressing: an idle consumer and a stuck one are identical from out here, and
// telling them apart means knowing whether there is work queued, which is the
// project's own business and the dependency ADR 0006 refused. So proximo reports
// and does not judge, the way a Check reports and never repairs. See
// docs/adr/0008-proximo-measures-the-project-concludes.md.
func writeRefusal(w io.Writer, readings []docker.Reading) {
	// Nothing running is nothing to refuse a conclusion about: the sentence is
	// about a container that is alive and may not be progressing.
	if len(readings) == 0 {
		return
	}
	fmt.Fprintln(w, "Whether that is wrong is not proximo's to say: a consumer with nothing to do and one blocked on a slow query look the same from outside the container, and only the project knows whether work was waiting.")
}
