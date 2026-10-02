package cli

import (
	"encoding/json"
	"testing"

	"github.com/filippolmt/proximo/internal/docker"
)

// TestStatusJSON: one entry per status row, carrying only the names the route
// actually answers on — a collision loser has no bare host, a flagged row none.
func TestStatusJSON(t *testing.T) {
	routes := []docker.Route{
		{Container: "shop-api-1", Host: "api.test", Qualified: "api.shop.test", URL: "https://api.test",
			Share: true, Peer: "api.m.mesh.internal", PeerQualified: "api.shop.m.mesh.internal"},
		{Container: "work-api-1", Host: "api.test", Qualified: "api.work.test", Note: "api.test is served by shop-api-1",
			Collision: true, CollisionOwner: "shop-api-1"},
		{Container: "db", Host: "db.test", Qualified: "db.shop.test", TCPPorts: []int{5432}, TLSMode: "terminate"},
		{Container: "web", Host: "web.test", Note: "starting"},
		{Container: "shop-docs-1", Host: "app.test", Qualified: "app.shop.test", Path: "/docs", URL: "https://app.test/docs"},
		{Container: "worker", Observed: true, Note: "no route — observed"},
	}
	got, err := json.Marshal(statusJSON(routes))
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
		t.Errorf("statusJSON =\n%s\nwant\n%s", got, want)
	}
}
