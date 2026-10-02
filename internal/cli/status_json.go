package cli

import "github.com/filippolmt/proximo/internal/docker"

// statusDoc is the `proximo status --json` document; its contract lives in
// docs/cli.md#--json.
type statusDoc struct {
	Routes []statusEntry `json:"routes"`
}

type statusEntry struct {
	Container string           `json:"container"`
	Scheme    string           `json:"scheme,omitempty"`
	Bare      string           `json:"bare,omitempty"`
	Qualified string           `json:"qualified,omitempty"`
	Path      string           `json:"path,omitempty"`
	Claimed   string           `json:"claimed,omitempty"`
	Peer      *statusPeer      `json:"peer,omitempty"`
	Collision *statusCollision `json:"collision,omitempty"`
	Warning   string           `json:"warning,omitempty"`
	Note      string           `json:"note,omitempty"`
}

type statusPeer struct {
	Bare      string `json:"bare,omitempty"`
	Qualified string `json:"qualified,omitempty"`
}

type statusCollision struct {
	Host     string `json:"host"`
	ServedBy string `json:"served_by"`
}

// statusJSON mirrors the table row for row, keeping the names a route answers
// on apart from the ones it claimed and does not: a row with a Note is not
// served under its Host. A Collision loser keeps the qualified host it still
// answers on; a flagged row (starting, ambiguous port) answers on nothing.
func statusJSON(routes []docker.Route) statusDoc {
	doc := statusDoc{Routes: []statusEntry{}}
	for _, r := range routes {
		e := statusEntry{Container: r.Container, Path: r.Path}
		switch {
		case r.Observed:
			e.Note = r.Note
		case r.Collision:
			e.Qualified = r.Qualified
			e.Collision = &statusCollision{Host: r.Host, ServedBy: r.CollisionOwner}
		case r.Note != "":
			e.Claimed = r.Host
			e.Warning = r.Note
		default:
			e.Scheme = "https"
			if r.IsTCP() {
				e.Scheme = "tcp"
			}
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
