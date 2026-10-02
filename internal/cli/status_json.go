package cli

import "github.com/filippolmt/proximo/internal/docker"

// The `proximo status --json` contract (docs/cli.md#proximo-status): additive
// only — a field is never renamed or removed — and a field is omitted when it
// has no value. It exists for tools that pin proximo's names themselves (e.g.
// a dev container's /etc/hosts): the derivation of a qualified host is
// proximo's, and a consumer re-deriving it from labels is how it went missing.
type statusDoc struct {
	Routes []statusEntry `json:"routes"`
}

type statusEntry struct {
	Container string          `json:"container"`
	Scheme    string          `json:"scheme,omitempty"`
	Bare      string          `json:"bare,omitempty"`
	Qualified string          `json:"qualified,omitempty"`
	Peer      *statusPeer     `json:"peer,omitempty"`
	Collision *statusConflict `json:"collision,omitempty"`
	Warning   string          `json:"warning,omitempty"`
	Note      string          `json:"note,omitempty"`
}

type statusPeer struct {
	Bare      string `json:"bare,omitempty"`
	Qualified string `json:"qualified,omitempty"`
}

type statusConflict struct {
	Host     string `json:"host"`
	ServedBy string `json:"served_by"`
}

// statusJSON mirrors the table row for row, listing only the names a route
// actually answers on: a row with a Note is not served under its Host, so it
// carries no bare host — a Collision loser keeps its qualified host, a flagged
// row (starting, ambiguous port) keeps nothing.
func statusJSON(routes []docker.Route) statusDoc {
	doc := statusDoc{Routes: []statusEntry{}}
	for _, r := range routes {
		e := statusEntry{Container: r.Container}
		switch {
		case r.Observed:
			e.Note = r.Note
		case r.Collision:
			e.Scheme = scheme(r)
			e.Qualified = r.Qualified
			e.Collision = &statusConflict{Host: r.Host, ServedBy: r.CollisionOwner}
		case r.Note != "":
			e.Warning = r.Note
		default:
			e.Scheme = scheme(r)
			e.Bare = r.Host
			e.Qualified = r.Qualified
		}
		if r.Peer != "" || r.PeerQualified != "" {
			e.Peer = &statusPeer{Bare: r.Peer, Qualified: r.PeerQualified}
		}
		doc.Routes = append(doc.Routes, e)
	}
	return doc
}

func scheme(r docker.Route) string {
	if len(r.TCPPorts) > 0 {
		return "tcp"
	}
	return "https"
}
