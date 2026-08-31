package inspect

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"
)

// accessLine is the slice of Traefik's JSON access log an Access record is made
// of. The field names are Traefik's; the rest of the line — client address,
// retry count, entrypoint — is deliberately not read: an Access record excludes
// what it excludes, and reading a field is how it starts being rendered.
type accessLine struct {
	StartUTC              time.Time `json:"StartUTC"`
	RequestHost           string    `json:"RequestHost"`
	RequestMethod         string    `json:"RequestMethod"`
	RequestPath           string    `json:"RequestPath"`
	DownstreamStatus      int       `json:"DownstreamStatus"`
	DownstreamContentSize int64     `json:"DownstreamContentSize"`
	Duration              int64     `json:"Duration"` // nanoseconds
	ServiceAddr           string    `json:"ServiceAddr"`
}

// DecodeAccessLine reads one line of Traefik's stdout as an Access record. It
// reports false for anything that is not one: Traefik's operational log shares
// the stream, and only access lines are JSON carrying a request.
func DecodeAccessLine(line []byte) (Exchange, bool) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] != '{' {
		return Exchange{}, false
	}
	var a accessLine
	if err := json.Unmarshal(line, &a); err != nil {
		return Exchange{}, false
	}
	// An operational line is JSON too. What tells them apart is that only an
	// access line describes a request.
	if a.RequestHost == "" || a.RequestMethod == "" || a.StartUTC.IsZero() {
		return Exchange{}, false
	}
	e := Exchange{
		At:       a.StartUTC,
		Host:     a.RequestHost,
		Method:   a.RequestMethod,
		Path:     a.RequestPath,
		Status:   a.DownstreamStatus,
		Duration: time.Duration(a.Duration),
		Bytes:    a.DownstreamContentSize,
		Backend:  a.ServiceAddr,
	}
	e.ID = DeriveID(e)
	return e, true
}

// DeriveID is the identity of an Exchange Traefik logged. Traefik mints none, so
// it is derived from what makes the Exchange unique — host, instant, backend —
// rather than drawn at random: two CLI invocations must name the same Exchange
// the same way, which is the only property an agent needs to say "that one".
func DeriveID(e Exchange) string {
	h := sha256.New()
	// Length-prefixed so no pair of fields can be shifted into another pair.
	for _, part := range []string{e.Host, e.At.UTC().Format(time.RFC3339Nano), e.Backend} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}
