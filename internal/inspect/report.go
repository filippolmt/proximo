package inspect

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The agent posts JSON proximo defines, so this file is one half of a contract
// whose other half is assets/agent.js. Keep them together: a field added there
// and not here is silently dropped.

// maxReportBody caps one report, whose bulk is the DOM Snapshot.
const maxReportBody = 32 << 20

// wireReport is exactly what the agent sends.
type wireReport struct {
	At          float64 `json:"at"` // epoch seconds
	Type        string  `json:"type"`
	Level       string  `json:"level"`
	Message     string  `json:"message"`
	File        string  `json:"file"`
	Line        int     `json:"line"`
	Col         int     `json:"col"`
	Stack       string  `json:"stack"`
	DOM         string  `json:"dom"`
	Breadcrumbs []struct {
		At       float64 `json:"at"`
		Category string  `json:"category"`
		Level    string  `json:"level"`
		Message  string  `json:"message"`
	} `json:"breadcrumbs"`
}

// ingested is what one POST carried. The Snapshot travels with the Report it
// belongs to rather than beside it, because nothing wants one without the other.
type ingested struct {
	Report   Report
	Snapshot []byte
	Found    bool
}

// stackFrame matches a line of a browser stack trace, in both the forms an engine
// produces: `at fn (url:line:col)` and the anonymous `at url:line:col`.
var stackFrame = regexp.MustCompile(`^\s*at\s+(?:(.+?)\s+\()?([^()\s]+?):(\d+):(\d+)\)?$`)

// decode turns one agent POST into a Client report.
func decode(b []byte) (ingested, error) {
	var w wireReport
	if err := json.Unmarshal(b, &w); err != nil {
		return ingested{}, fmt.Errorf("report payload: %w", err)
	}
	if w.Message == "" && w.Stack == "" {
		return ingested{}, nil // nothing worth recording; Found stays false
	}

	r := Report{
		At:      epoch(w.At),
		Type:    or(w.Type, "Error"),
		Level:   or(w.Level, "error"),
		Message: w.Message,
		Stack:   w.Stack,
		Frames:  framesOf(w.Stack),
	}
	// window.onerror hands over a location even when there is no usable stack —
	// a cross-origin script, or an engine that produced none. Do not lose it.
	if len(r.Frames) == 0 && w.File != "" {
		r.Frames = []Frame{{File: w.File, Line: w.Line, Col: w.Col}}
	}
	for _, c := range w.Breadcrumbs {
		r.Breadcrumbs = append(r.Breadcrumbs, Breadcrumb{
			At: epoch(c.At), Category: or(c.Category, "unknown"),
			Level: or(c.Level, "info"), Message: c.Message,
		})
	}
	return ingested{Report: r, Snapshot: []byte(w.DOM), Found: true}, nil
}

// framesOf reads the frames out of a stack trace, dropping the agent's own: it is
// served from the page's origin, so its frames would otherwise sit at the bottom
// of every stack, pointing the reader at proximo instead of at their bug. The raw
// stack is kept on the Report either way, so nothing parsed away is lost.
func framesOf(stack string) []Frame {
	var out []Frame
	for _, line := range strings.Split(stack, "\n") {
		m := stackFrame.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if strings.Contains(m[2], ReservedPath) {
			continue
		}
		lineNo, _ := strconv.Atoi(m[3])
		col, _ := strconv.Atoi(m[4])
		out = append(out, Frame{File: m[2], Line: lineNo, Col: col, Func: m[1]})
	}
	return out
}

func epoch(secs float64) time.Time {
	if secs <= 0 {
		return time.Now().UTC()
	}
	return time.UnixMilli(int64(secs * 1000)).UTC()
}

func or(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}
