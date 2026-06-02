// Command proximo is a local development reverse proxy that makes any running
// Docker container reachable at https://<name>.<tld> with automatic DNS and
// trusted HTTPS, on macOS and Linux.
package main

import "github.com/filippolmt/proximo/internal/cli"

func main() {
	cli.Execute()
}
