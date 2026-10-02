package docker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestNewInventory: one entry per status row, carrying only the names the route
// actually answers on — a collision loser has no bare host, a flagged row none.
func TestNewInventory(t *testing.T) {
	routes := []Route{
		{Container: "shop-api-1", Host: "api.test", Qualified: "api.shop.test", URL: "https://api.test",
			Share: true, Peer: "api.m.mesh.internal", PeerQualified: "api.shop.m.mesh.internal"},
		{Container: "work-api-1", Host: "api.test", Qualified: "api.work.test", Note: "api.test is served by shop-api-1",
			Collision: true, CollisionOwner: "shop-api-1"},
		{Container: "db", Host: "db.test", Qualified: "db.shop.test", TCPPorts: []int{5432}, TLSMode: "terminate"},
		{Container: "web", Host: "web.test", Note: "starting"},
		{Container: "shop-docs-1", Host: "app.test", Qualified: "app.shop.test", Path: "/docs", URL: "https://app.test/docs"},
		{Container: "worker", Observed: true, Note: "no route — observed"},
	}
	got, err := json.Marshal(NewInventory(routes))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"routes":[` +
		`{"container":"shop-api-1","scheme":"https","bare":"api.test","qualified":"api.shop.test","peer":{"bare":"api.m.mesh.internal","qualified":"api.shop.m.mesh.internal"}},` +
		`{"container":"work-api-1","qualified":"api.work.test","collision":{"host":"api.test","served_by":"shop-api-1"}},` +
		`{"container":"db","scheme":"tcp","bare":"db.test","qualified":"db.shop.test"},` +
		`{"container":"web","claimed":"web.test","warning":"starting"},` +
		`{"container":"shop-docs-1","scheme":"https","bare":"app.test","qualified":"app.shop.test","path":"/docs"},` +
		`{"container":"worker","note":"no route — observed"}]}`
	if string(got) != want {
		t.Errorf("NewInventory =\n%s\nwant\n%s", got, want)
	}
}

// TestWriteInventory: the file is replaced only when its content changes, so a
// periodic reconcile does not churn a consumer watching it, and an empty
// inventory is written as one rather than left out.
func TestWriteInventory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, InventoryFile)
	inv := NewInventory([]Route{{Container: "a", Host: "a.test", URL: "https://a.test"}})
	if changed, err := WriteInventory(dir, inv); err != nil || !changed {
		t.Fatalf("first write: changed=%v err=%v, want a write", changed, err)
	}
	if changed, err := WriteInventory(dir, inv); err != nil || changed {
		t.Fatalf("same content: changed=%v err=%v, want no write", changed, err)
	}
	if _, err := WriteInventory(dir, NewInventory(nil)); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got Inventory
	if err := json.Unmarshal(raw, &got); err != nil || got.Routes == nil || len(got.Routes) != 0 {
		t.Fatalf("empty inventory = %s (%v), want {\"routes\": []}", raw, err)
	}
}
