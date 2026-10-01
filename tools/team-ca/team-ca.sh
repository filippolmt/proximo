#!/bin/sh
# team-ca.sh — run the custodian's tool (tools/team-ca) with Docker as the only
# prerequisite. Every file argument is a path inside the current directory,
# which is mounted into the container; output lands there or on stdout.
#
#   tools/team-ca/team-ca.sh root <suffix>
#   tools/team-ca/team-ca.sh sign <suffix> <machine> team-root.crt team-root.key machine.csr > intermediate.crt
#
# Work in a temporary directory, and remove the plaintext root key when done.
set -eu
repo=$(cd "$(dirname "$0")/../.." && pwd)
exec docker run --rm -i --user "$(id -u):$(id -g)" -e HOME=/tmp \
	-v "$repo":/src:ro -v "$PWD":/work \
	"${GO_IMAGE:-golang:1.27-alpine}" \
	sh -c 'cd /src && go build -buildvcs=false -o /tmp/team-ca ./tools/team-ca && cd /work && exec /tmp/team-ca "$@"' team-ca "$@"
