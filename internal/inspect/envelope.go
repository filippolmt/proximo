package inspect

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The agent is @sentry/browser pointed at the reserved path with its `tunnel`
// option, so what arrives here is a Sentry envelope: a header line, then one
// header line plus one payload per item. The wire format is public and stable
// (https://develop.sentry.dev/sdk/envelopes/); parsing it is the price of not
// writing the browser agent ourselves.

// snapshotFilename is the attachment the injected snippet uses for the DOM
// Snapshot. Sentry's own SDK does not capture DOM without its replay product,
// so the snippet attaches it explicitly.
const snapshotFilename = "dom.html"

// maxItemLength caps a declared item length so a malformed header cannot make us
// allocate arbitrarily. Sentry's own ingest limit for a single item is 1 MiB;
// the DOM Snapshot needs more headroom than that.
const maxItemLength = 32 << 20

type envelopeItem struct {
	Type     string
	Filename string
	Payload  []byte
}

// parseEnvelope splits a Sentry envelope into its items. The envelope header
// itself carries nothing we need — the correlation id travels in the tunnel URL,
// not in the payload — so it is validated and discarded.
func parseEnvelope(b []byte) ([]envelopeItem, error) {
	line, rest, ok := bytes.Cut(b, []byte("\n"))
	if !ok {
		return nil, fmt.Errorf("envelope: no header line")
	}
	if !json.Valid(line) {
		return nil, fmt.Errorf("envelope: malformed header")
	}

	var items []envelopeItem
	for len(rest) > 0 {
		var hdrLine []byte
		hdrLine, rest, ok = bytes.Cut(rest, []byte("\n"))
		if !ok && len(bytes.TrimSpace(hdrLine)) == 0 {
			break // trailing newline
		}
		if len(bytes.TrimSpace(hdrLine)) == 0 {
			continue
		}

		var hdr struct {
			Type     string `json:"type"`
			Length   *int   `json:"length"`
			Filename string `json:"filename"`
		}
		if err := json.Unmarshal(hdrLine, &hdr); err != nil {
			return nil, fmt.Errorf("envelope: item header: %w", err)
		}

		var payload []byte
		if hdr.Length != nil {
			// An explicit length is authoritative: the payload may itself
			// contain newlines, which is exactly why the header carries one.
			n := *hdr.Length
			if n < 0 || n > maxItemLength || n > len(rest) {
				return nil, fmt.Errorf("envelope: item %q declares length %d", hdr.Type, n)
			}
			payload, rest = rest[:n], rest[n:]
			rest = bytes.TrimPrefix(rest, []byte("\n"))
		} else {
			payload, rest, _ = bytes.Cut(rest, []byte("\n"))
		}
		items = append(items, envelopeItem{Type: hdr.Type, Filename: hdr.Filename, Payload: payload})
	}
	return items, nil
}

// sentryEvent is the subset of a Sentry event payload a Client report is built
// from. Everything the SDK sends that proximo has no use for is ignored.
type sentryEvent struct {
	Timestamp sentryTime `json:"timestamp"`
	Level     string     `json:"level"`
	Message   string     `json:"message"`
	Logentry  struct {
		Message string `json:"message"`
	} `json:"logentry"`
	Exception struct {
		Values []struct {
			Type       string `json:"type"`
			Value      string `json:"value"`
			Stacktrace struct {
				Frames []struct {
					Filename string `json:"filename"`
					Function string `json:"function"`
					Lineno   int    `json:"lineno"`
					Colno    int    `json:"colno"`
				} `json:"frames"`
			} `json:"stacktrace"`
		} `json:"values"`
	} `json:"exception"`
	Breadcrumbs struct {
		Values []struct {
			Timestamp sentryTime `json:"timestamp"`
			Category  string     `json:"category"`
			Level     string     `json:"level"`
			Message   string     `json:"message"`
			Type      string     `json:"type"`
		} `json:"values"`
	} `json:"breadcrumbs"`
}

// sentryTime accepts both shapes Sentry SDKs emit for a timestamp: epoch seconds
// as a number (what the browser SDK sends) and an RFC 3339 string.
type sentryTime struct{ time.Time }

func (t *sentryTime) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		return nil
	}
	if secs, err := strconv.ParseFloat(s, 64); err == nil {
		sec, frac := int64(secs), secs-float64(int64(secs))
		t.Time = time.Unix(sec, int64(frac*float64(time.Second))).UTC()
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return fmt.Errorf("timestamp %q: %w", s, err)
	}
	t.Time = parsed.UTC()
	return nil
}

// reportFrom builds a Client report out of a Sentry event payload.
func reportFrom(payload []byte) (Report, error) {
	var ev sentryEvent
	if err := json.Unmarshal(payload, &ev); err != nil {
		return Report{}, fmt.Errorf("event payload: %w", err)
	}

	r := Report{At: ev.Timestamp.Time, Level: ev.Level, Type: "message", Message: ev.Message}
	if r.At.IsZero() {
		r.At = time.Now().UTC()
	}
	if r.Level == "" {
		r.Level = "error"
	}
	if r.Message == "" {
		r.Message = ev.Logentry.Message
	}

	// Sentry nests chained exceptions oldest-first, so the last value is the one
	// actually thrown — the one a developer is looking for.
	if vals := ev.Exception.Values; len(vals) > 0 {
		exc := vals[len(vals)-1]
		r.Type = exc.Type
		r.Message = strings.TrimSpace(exc.Type + ": " + exc.Value)
		// Frames arrive oldest-first; the innermost call is what failed, so it
		// goes first. The agent's own frames are dropped: it is served from the
		// page's origin, so the SDK does not recognise itself as SDK code and
		// leaves a frame pointing at proximo at the bottom of every stack —
		// noise that points the reader away from the actual bug.
		frames := exc.Stacktrace.Frames
		for i := len(frames) - 1; i >= 0; i-- {
			f := frames[i]
			if strings.Contains(f.Filename, ReservedPath) {
				continue
			}
			r.Frames = append(r.Frames, Frame{File: f.Filename, Line: f.Lineno, Col: f.Colno, Func: f.Function})
		}
	}

	for _, b := range ev.Breadcrumbs.Values {
		level := b.Level
		if level == "" {
			level = "info"
		}
		category := b.Category
		if category == "" {
			category = b.Type
		}
		r.Breadcrumbs = append(r.Breadcrumbs, Breadcrumb{
			At: b.Timestamp.Time, Category: category, Level: level, Message: b.Message,
		})
	}
	return r, nil
}

// decode turns one envelope into the Client report it carries and the DOM
// Snapshot attached to it. An envelope with no event item — a session or a
// standalone attachment — yields ok=false and is dropped.
func decode(b []byte) (r Report, snapshot []byte, ok bool, err error) {
	items, err := parseEnvelope(b)
	if err != nil {
		return Report{}, nil, false, err
	}
	for _, it := range items {
		switch {
		case it.Type == "event":
			if r, err = reportFrom(it.Payload); err != nil {
				return Report{}, nil, false, err
			}
			ok = true
		case it.Type == "attachment" && it.Filename == snapshotFilename:
			snapshot = it.Payload
		}
	}
	return r, snapshot, ok, nil
}
