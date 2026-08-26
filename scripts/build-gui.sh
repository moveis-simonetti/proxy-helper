#!/usr/bin/env bash
# Build the GUI binary inside a container with an older base than the host,
# so the result runs on distributions older than this machine.
#
# The GUI cannot be statically linked the way the CLI is: it needs GTK3 and
# libayatana-appindicator through cgo, so the binary is dynamically linked
# and carries a glibc floor. glibc is backward compatible but not forward
# compatible — a binary built against 2.39 refuses to start on 2.35. So the
# floor is set by the OLDEST base we build on, not by the newest we support.
#
# ubuntu:22.04 (glibc 2.35) is the default because it covers Linux Mint 21
# and 22, and Mint 20 reached end of life in April 2025. Override with
# BASE_IMAGE for something else.
#
# Usage:
#   scripts/build-gui.sh
#   BASE_IMAGE=ubuntu:20.04 scripts/build-gui.sh
set -euo pipefail

cd "$(dirname "$0")/.."

BASE_IMAGE="${BASE_IMAGE:-ubuntu:22.04}"
GO_VERSION="$(sed -n 's/^go //p' go.mod)"
GOARCH="$(go env GOARCH)"
OUT_DIR="dist"
OUT_NAME="proxy-helper-gui_linux_${GOARCH}"

if [ -z "$GO_VERSION" ]; then
	echo "could not read the Go version from go.mod" >&2
	exit 1
fi

mkdir -p "$OUT_DIR"

# The module cache is mounted so a rebuild does not re-download the world,
# and the container runs as the invoking user so nothing lands root-owned in
# the working tree.
GOMODCACHE="$(go env GOMODCACHE)"
mkdir -p "$GOMODCACHE"

echo "base image : $BASE_IMAGE"
echo "go version : $GO_VERSION"
echo "output     : $OUT_DIR/$OUT_NAME"

docker run --rm \
	-v "$PWD:/src" \
	-v "$GOMODCACHE:/gomodcache" \
	-w /src \
	-e "GO_VERSION=$GO_VERSION" \
	-e "OUT=/src/$OUT_DIR/$OUT_NAME" \
	-e "HOST_UID=$(id -u)" \
	-e "HOST_GID=$(id -g)" \
	"$BASE_IMAGE" \
	bash -euo pipefail -c '
		export DEBIAN_FRONTEND=noninteractive
		apt-get update -qq
		apt-get install -y -qq --no-install-recommends \
			ca-certificates curl build-essential pkg-config \
			libgtk-3-dev libayatana-appindicator3-dev >/dev/null

		arch="$(dpkg --print-architecture)"
		curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-${arch}.tar.gz" \
			| tar -C /usr/local -xz
		export PATH=/usr/local/go/bin:$PATH
		export GOMODCACHE=/gomodcache
		export GOFLAGS=-buildvcs=false
		export CGO_ENABLED=1

		go build -tags gui -ldflags "-s -w" -o "$OUT" .
		chown "$HOST_UID:$HOST_GID" "$OUT"

		echo "--- glibc floor ---"
		objdump -T "$OUT" | grep -o "GLIBC_[0-9.]*" | sort -Vu | tail -1
	'

echo
echo "built: $OUT_DIR/$OUT_NAME"
