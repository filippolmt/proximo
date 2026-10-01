package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/filippolmt/proximo/internal/docker"
	"github.com/filippolmt/proximo/internal/inspect"
	"github.com/filippolmt/proximo/internal/transcript"
)

var errorsPeer = docker.PeerNames{Machine: "studio-01", Suffix: "mesh.internal"}

func TestMarkPeer(t *testing.T) {
	exchanges := []inspect.Exchange{{Host: "api.test"}, {Host: "api.shop.studio-01.mesh.internal"}}
	markPeer(exchanges, errorsPeer)
	if exchanges[0].Peer || !exchanges[1].Peer {
		t.Errorf("peer = %v, %v", exchanges[0].Peer, exchanges[1].Peer)
	}
	// Unconfigured, nothing is a peer name.
	exchanges[1].Peer = false
	markPeer(exchanges, docker.PeerNames{})
	if exchanges[1].Peer {
		t.Error("marked a peer name on a machine with no suffix")
	}
}

// --json carries "peer": true on an Exchange that arrived on a peer name and
// omits it otherwise; the suffix itself is not added.
func TestExchangeJSONPeer(t *testing.T) {
	local, _ := json.Marshal(inspect.Exchange{Host: "api.test"})
	peer, _ := json.Marshal(inspect.Exchange{Host: "api.studio-01.mesh.internal", Peer: true})
	if strings.Contains(string(local), `"peer"`) || !strings.Contains(string(peer), `"peer":true`) {
		t.Errorf("local = %s\npeer = %s", local, peer)
	}
}

// The reading row appends the host only when it is a peer name; a local row is
// unchanged.
func TestWriteExchangePeerHost(t *testing.T) {
	at := time.Date(2026, 10, 1, 9, 0, 0, 0, time.Local)
	var local, peer bytes.Buffer
	writeExchange(&local, inspect.Exchange{ID: "ex1", At: at, Host: "api.test", Method: "GET", Path: "/", Status: 200}, transcript.Transcript{}, everything)
	writeExchange(&peer, inspect.Exchange{ID: "ex2", At: at, Host: "api.studio-01.mesh.internal", Method: "GET", Path: "/", Status: 200, Peer: true}, transcript.Transcript{}, everything)
	first := func(b bytes.Buffer) string { return strings.SplitN(b.String(), "\n", 2)[0] }
	if strings.Contains(first(local), "api.test") {
		t.Errorf("local row = %q", first(local))
	}
	if !strings.HasSuffix(first(peer), "  api.studio-01.mesh.internal") {
		t.Errorf("peer row = %q", first(peer))
	}
}

func TestPeerExclusionNote(t *testing.T) {
	now := time.Now()
	window := []inspect.Exchange{
		{Host: "api.test", At: now, Status: 500},
		{Host: "api.studio-01.mesh.internal", At: now, Status: 500},
		{Host: "api.studio-01.mesh.internal", At: now, Status: 502},
	}
	note := peerExclusionNote(window, "api.test", "test", errorsPeer, now.Add(-time.Hour), false, func([]inspect.Exchange) string { return "shop/api" })
	if !strings.Contains(note, "2 Exchange") || !strings.Contains(note, "api.studio-01.mesh.internal") || !strings.Contains(note, "--service shop/api") {
		t.Errorf("note = %q", note)
	}
	if n := peerExclusionNote(window[:1], "api.test", "test", errorsPeer, now.Add(-time.Hour), false, func([]inspect.Exchange) string { return "shop/api" }); n != "" {
		t.Errorf("a note with no peer Exchange: %q", n)
	}
	if n := peerExclusionNote(window, "api.test", "test", docker.PeerNames{}, now.Add(-time.Hour), false, func([]inspect.Exchange) string { return "" }); n != "" {
		t.Errorf("a note on an unconfigured machine: %q", n)
	}
}
