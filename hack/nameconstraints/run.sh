#!/bin/sh
# Drives the name-constraint proof on macOS: mints the fixtures, makes the
# names resolve, installs the team root into the stores proximo writes, and —
# the part that matters — takes all of it back out again.
#
# The root it installs is a real CA in a real trust store. It stays there until
# `teardown` removes it. Nothing else in this script needs undoing.
#
# Ubuntu's four trust stores are in README.md; this script is Darwin only.
set -eu

SUFFIX=mesh.internal
MACHINE=machine-a
ROOT_CN='proof team root (name-constrained)'
MARKER=proximo-nc-proof
KEYCHAIN=/Library/Keychains/System.keychain
GO_IMAGE=golang:1.27-alpine
OUT=proof-out
CONTAINER=proximo-nc-proof

IN_SUBTREE="app.$MACHINE.$SUFFIX"
DNS_OUT=out-of-subtree.example.com

case "$(uname -s)" in
Darwin) ;;
*) echo "this script is Darwin only — see README.md for Ubuntu" >&2; exit 1 ;;
esac

# The path internal/tls/nss.go globs. It contains a space, so the glob is only
# ever expanded against a quoted prefix — never through a command substitution,
# which would split "Application Support" in two.
FF_PROFILES="$HOME/Library/Application Support/Firefox/Profiles"

stop_server() {
	docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
}

mint() {
	docker run --rm -v "$PWD":/src -w /src -e CGO_ENABLED=0 "$GO_IMAGE" \
		go run ./hack/nameconstraints -mint -verify \
		-suffix "$SUFFIX" -machine "$MACHINE" -out "/src/$OUT"
}

hosts_add() {
	if grep -q "$MARKER" /etc/hosts; then
		echo "/etc/hosts: already present"
		return
	fi
	echo "/etc/hosts: adding two names (sudo)"
	printf '127.0.0.1 %s # %s\n127.0.0.1 %s # %s\n' \
		"$IN_SUBTREE" "$MARKER" "$DNS_OUT" "$MARKER" |
		sudo tee -a /etc/hosts >/dev/null
}

hosts_remove() {
	if grep -q "$MARKER" /etc/hosts; then
		echo "/etc/hosts: removing the two names (sudo)"
		sudo sed -i '' "/$MARKER/d" /etc/hosts
	fi
}

trust_install() {
	echo "keychain: installing the root as a trusted root (sudo)"
	sudo security add-trusted-cert -d -r trustRoot -k "$KEYCHAIN" "$OUT/root.pem"

	if ! command -v certutil >/dev/null 2>&1; then
		echo "certutil missing — Firefox row cannot be filled; run: brew install nss" >&2
		return
	fi
	found=0
	for profile in "$FF_PROFILES"/*; do
		[ -f "$profile/cert9.db" ] || continue
		found=1
		echo "firefox: installing the root into $profile"
		certutil -D -d "sql:$profile" -n "$ROOT_CN" 2>/dev/null || true
		certutil -A -d "sql:$profile" -t C,, -n "$ROOT_CN" -i "$OUT/root.pem"
	done
	[ "$found" -eq 1 ] ||
		echo "no Firefox NSS database — install Firefox, open it once, then re-run" >&2
}

trust_remove() {
	echo "keychain: removing the root (sudo)"
	sudo security delete-certificate -c "$ROOT_CN" "$KEYCHAIN" 2>/dev/null || true
	command -v certutil >/dev/null 2>&1 || return 0
	for profile in "$FF_PROFILES"/*; do
		[ -f "$profile/cert9.db" ] || continue
		echo "firefox: removing the root from $profile"
		certutil -D -d "sql:$profile" -n "$ROOT_CN" 2>/dev/null || true
	done
}

urls() {
	cat <<TXT

Open each URL in Safari, Chrome and Firefox, and record the exact error text.

  accepted   https://$IN_SUBTREE:8443/
  rejected   https://$DNS_OUT:8444/
  rejected   https://127.0.0.1:8445/

The accepted case is the control: without it, a rejection of the other two is
equally well explained by the root not being trusted at all.

Chrome first: chrome://version must be 126 or newer, and chrome://policy must
not set EnforceLocalAnchorConstraintsEnabled. Below 126 the reading proves
nothing.

Do NOT click through a warning. Record what the browser said and close the tab.
Safari stores a click-through as a trust exception for that certificate, which
would make the same case read as accepted on every later round.
TXT
}

case "${1-}" in
setup)
	# New fixtures mean a new root, so anything still serving the old ones would
	# be answering with certificates the installed root cannot vouch for.
	stop_server
	mint
	hosts_add
	urls
	echo
	echo "Now read the three URLs with the root NOT yet installed — that is the"
	echo "ordinary first-run state the proof also has to record. Then:"
	echo "  $0 trust     # install the root, and read them again"
	;;
trust)
	trust_install
	urls
	;;
serve)
	stop_server
	docker run -d --name "$CONTAINER" \
		-v "$PWD":/src -w /src -e CGO_ENABLED=0 \
		-p 127.0.0.1:8443:8443 -p 127.0.0.1:8444:8444 -p 127.0.0.1:8445:8445 \
		"$GO_IMAGE" go run ./hack/nameconstraints -serve -out "/src/$OUT" >/dev/null
	echo "serving in the background as $CONTAINER"
	urls
	echo
	echo "  $0 logs      # what each browser did at the TLS layer"
	echo "  $0 stop      # stop serving"
	;;
logs)
	docker logs -f "$CONTAINER"
	;;
stop)
	stop_server
	echo "stopped"
	;;
teardown)
	stop_server
	trust_remove
	hosts_remove
	rm -rf "$OUT"
	echo "done — the root is out of both stores and the fixtures are deleted"
	;;
*)
	echo "usage: $0 setup | serve | trust | logs | stop | teardown" >&2
	echo "  setup     mint the fixtures and make the names resolve" >&2
	echo "  serve     serve the three leaves in the background" >&2
	echo "  trust     install the root into the keychain and Firefox" >&2
	echo "  logs      follow the server log" >&2
	echo "  stop      stop serving" >&2
	echo "  teardown  stop, remove the root from both stores, delete the fixtures" >&2
	exit 1
	;;
esac
