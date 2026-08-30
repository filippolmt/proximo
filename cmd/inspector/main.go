// Command inspector is the proximo hop that serves routes under Inspection. It
// proxies each request to the backend Traefik names in X-Proximo-Backend,
// injects the reporting agent into HTML on the way back, and keeps the resulting
// Exchanges in memory. It is built into a container image and sits in the
// request path only for containers labelled proximo.inspect.
//
// Two listeners, deliberately: the proxy is reachable from the browser and
// exposes nothing but the agent and its ingest endpoint, while the read API —
// which can hand back every Exchange recorded — is published on loopback only,
// for the proximo CLI.
package main

import (
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/filippolmt/proximo/internal/inspect"
)

func main() {
	addr := getenv("PROXIMO_INSPECT_ADDR", ":9000")
	adminAddr := getenv("PROXIMO_INSPECT_ADMIN_ADDR", ":9001")

	budget := int64(inspect.DefaultBudget)
	if v := os.Getenv("PROXIMO_INSPECT_BUDGET"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			log.Fatalf("proximo inspect: PROXIMO_INSPECT_BUDGET=%q: %v", v, err)
		}
		budget = n
	}

	agent, err := inspect.Agent()
	if err != nil {
		log.Fatalf("proximo inspect: %v", err)
	}

	store := inspect.NewStore(budget)
	go func() {
		log.Printf("proximo inspect: read API on %s", adminAddr)
		log.Fatal(http.ListenAndServe(adminAddr, inspect.AdminHandler{Store: store}))
	}()

	log.Printf("proximo inspect: proxying inspected routes on %s (%d MiB of Exchanges)", addr, budget>>20)
	log.Fatal(http.ListenAndServe(addr, inspect.NewHandler(store, agent)))
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
