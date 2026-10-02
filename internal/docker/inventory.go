package docker

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
)

// InventoryFile is the inventory's name inside config.InventoryDir.
const InventoryFile = "routes.json"

// Inventory is the effective-route inventory for tools rather than people: the
// `proximo status --json` document, and the file the watcher keeps current. Its
// contract lives in docs/cli.md#--json.
type Inventory struct {
	Routes []InventoryEntry `json:"routes"`
}

// InventoryEntry is one status row.
type InventoryEntry struct {
	Container string              `json:"container"`
	Scheme    string              `json:"scheme,omitempty"`
	Bare      string              `json:"bare,omitempty"`
	Qualified string              `json:"qualified,omitempty"`
	Path      string              `json:"path,omitempty"`
	Claimed   string              `json:"claimed,omitempty"`
	Peer      *InventoryPeer      `json:"peer,omitempty"`
	Collision *InventoryCollision `json:"collision,omitempty"`
	Warning   string              `json:"warning,omitempty"`
	Note      string              `json:"note,omitempty"`
}

// InventoryPeer is the peer names a row's hosts answer on.
type InventoryPeer struct {
	Bare      string `json:"bare,omitempty"`
	Qualified string `json:"qualified,omitempty"`
}

// InventoryCollision is the host a row lost and the claimant that kept it.
type InventoryCollision struct {
	Host     string `json:"host"`
	ServedBy string `json:"served_by"`
}

// NewInventory mirrors the status rows one for one, keeping the names a route
// answers on apart from the ones it claimed and does not: a row with a Note is
// not served under its Host (Route.Kind). A Collision loser keeps the qualified host it
// still answers on; a flagged row (starting, ambiguous port) answers on nothing.
func NewInventory(routes []Route) Inventory {
	inv := Inventory{Routes: []InventoryEntry{}}
	for _, r := range routes {
		e := InventoryEntry{Container: r.Container, Path: r.Path}
		switch r.Kind() {
		case RowObserved:
			e.Note = r.Note
		case RowCollision:
			e.Qualified = r.Qualified
			e.Peer = inventoryPeer(r)
			e.Collision = &InventoryCollision{Host: r.Host, ServedBy: r.CollisionOwner}
		case RowFlagged:
			e.Claimed = r.Host
			e.Warning = r.Note
		case RowServed:
			e.Scheme = "https"
			if r.IsTCP() {
				e.Scheme = "tcp"
			}
			e.Bare = r.Host
			e.Qualified = r.Qualified
			e.Peer = inventoryPeer(r)
		}
		inv.Routes = append(inv.Routes, e)
	}
	return inv
}

// inventoryPeer is a row's peer names, nil when it has none. Only a row that
// answers on a name carries any: a flagged or observed row never does.
func inventoryPeer(r Route) *InventoryPeer {
	if r.Peer == "" && r.PeerQualified == "" {
		return nil
	}
	return &InventoryPeer{Bare: r.Peer, Qualified: r.PeerQualified}
}

// MarshalIndent is the inventory's one encoding, shared by the CLI and the file
// so the two are byte-identical.
func (inv Inventory) MarshalIndent() ([]byte, error) {
	data, err := json.MarshalIndent(inv, "", "  ")
	return append(data, '\n'), err
}

// WriteInventory replaces dir's inventory file atomically, and only when its
// content changed: readers never see a truncated document, and a periodic
// reconcile does not churn a consumer watching the file.
func WriteInventory(dir string, inv Inventory) (changed bool, err error) {
	data, err := inv.MarshalIndent()
	if err != nil {
		return false, err
	}
	path := filepath.Join(dir, InventoryFile)
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		return false, nil
	}
	return true, atomicWrite(path, data, 0o644)
}
