#!/bin/sh
# ceremony.sh — enrol the sharing machine (A) in a throwaway peer-sharing lab:
# mint a team root, configure the peer values, sign this machine's
# intermediate, trust the root and bring the stack up. Run it on A, from
# anywhere in the repository checkout, with Docker and proximo installed.
#
#   hack/netbird-lab/ceremony.sh <machine> <suffix> <mesh-address> [lab-dir]
#   hack/netbird-lab/ceremony.sh studio-01 mesh.internal 100.64.0.10
#
# The lab directory (default ~/proximo-netbird-lab) keeps team-root.crt, the
# file the colleague's machine trusts. The plaintext root key is removed at the
# end: this root exists for one lab. A rerun needs an empty lab directory.
set -eu
[ $# -ge 3 ] || { sed -n '7,8p' "$0" >&2; exit 2; }
machine=$1 suffix=$2 address=$3
lab=${4:-$HOME/proximo-netbird-lab}
team_ca=$(cd "$(dirname "$0")/../.." && pwd)/tools/team-ca/team-ca.sh

mkdir -p "$lab"
cd "$lab"
"$team_ca" root "$suffix"

proximo config peer-suffix "$suffix"
proximo config machine "$machine"
proximo config address "$address"
proximo config team-root "$lab/team-root.crt"
proximo config csr > machine.csr
"$team_ca" sign "$suffix" "$machine" team-root.crt team-root.key machine.csr > intermediate.crt
proximo config intermediate "$lab/intermediate.crt"
rm -f team-root.key

proximo trust
proximo up
proximo doctor || true

cat <<EOF

Done. peer-dns must pass before the nameserver group is created; mesh fails
until then. Copy $lab/team-root.crt to the colleague's machine.
EOF
