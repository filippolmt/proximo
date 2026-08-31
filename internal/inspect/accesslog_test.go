package inspect

import (
	"testing"
	"time"
)

// A real Traefik v3 access line, JSON format, default fields. It is the
// independent source of truth for this decoder: the field names are Traefik's,
// not ours.
const traefikLine = `{"ClientAddr":"172.18.0.1:54321","ClientHost":"172.18.0.1","ClientPort":"54321","ClientUsername":"-","DownstreamContentSize":1521,"DownstreamStatus":500,"Duration":41234567,"OriginContentSize":1521,"OriginDuration":40000000,"OriginStatus":500,"Overhead":1234567,"RequestAddr":"web.test","RequestContentSize":0,"RequestCount":7,"RequestHost":"web.test","RequestMethod":"POST","RequestPath":"/checkout","RequestPort":"-","RequestProtocol":"HTTP/1.1","RequestScheme":"https","RetryAttempts":0,"RouterName":"proximo-web@file","ServiceAddr":"web-1:8080","ServiceName":"proximo-web@file","ServiceURL":"http://web-1:8080","StartUTC":"2026-08-31T10:00:00.123456789Z","entryPointName":"websecure","level":"info","msg":"","time":"2026-08-31T10:00:00Z"}`

func TestDecodeAccessLine(t *testing.T) {
	e, ok := DecodeAccessLine([]byte(traefikLine))
	if !ok {
		t.Fatal("a Traefik access line was not recognised")
	}
	want := time.Date(2026, 8, 31, 10, 0, 0, 123456789, time.UTC)
	if !e.At.Equal(want) {
		t.Errorf("At = %v, want %v", e.At, want)
	}
	if e.Host != "web.test" || e.Method != "POST" || e.Path != "/checkout" {
		t.Errorf("request = %s %s %s", e.Host, e.Method, e.Path)
	}
	if e.Status != 500 {
		t.Errorf("Status = %d, want 500", e.Status)
	}
	if e.Duration != 41234567*time.Nanosecond {
		t.Errorf("Duration = %v, want 41.234567ms", e.Duration)
	}
	if e.Bytes != 1521 {
		t.Errorf("Bytes = %d, want 1521", e.Bytes)
	}
	// The one field a Transcript is looked up from.
	if e.Backend != "web-1:8080" {
		t.Errorf("Backend = %q, want web-1:8080", e.Backend)
	}
}

// Traefik's operational log and its access log share one stream. Only the
// access lines are JSON, and only they carry a request.
func TestDecodeAccessLineRejectsTheOperationalLog(t *testing.T) {
	for _, line := range []string{
		`time="2026-08-31T10:00:00Z" level=info msg="Configuration loaded from file."`,
		`{"level":"info","msg":"Traefik version 3.6.0 built on 2026-01-01","time":"2026-08-31T10:00:00Z"}`,
		``,
		`   `,
		`not json at all`,
	} {
		if _, ok := DecodeAccessLine([]byte(line)); ok {
			t.Errorf("decoded a non-access line as an Exchange: %s", line)
		}
	}
}

// An Exchange Traefik logged has no minted identity — Traefik stamps none. It is
// derived, and must be the same derivation in two CLI invocations: an agent has
// to be able to say "that one".
func TestDerivedIdentityIsStableAndDistinguishing(t *testing.T) {
	a, _ := DecodeAccessLine([]byte(traefikLine))
	b, _ := DecodeAccessLine([]byte(traefikLine))
	if a.ID == "" {
		t.Fatal("a decoded Exchange has no identity")
	}
	if a.ID != b.ID {
		t.Errorf("identity is not stable: %q then %q", a.ID, b.ID)
	}
	if len(a.ID) != 16 {
		t.Errorf("identity %q is not the 16-hex shape a minted one has", a.ID)
	}

	// Host, instant and backend are what it is derived from: change any one and
	// it must be a different Exchange.
	for name, ex := range map[string]Exchange{
		"host":    {At: a.At, Host: "other.test", Backend: a.Backend},
		"instant": {At: a.At.Add(time.Nanosecond), Host: a.Host, Backend: a.Backend},
		"backend": {At: a.At, Host: a.Host, Backend: "web-2:8080"},
	} {
		if got := DeriveID(ex); got == a.ID {
			t.Errorf("a different %s produced the same identity %q", name, got)
		}
	}
}
