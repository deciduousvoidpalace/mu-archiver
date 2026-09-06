#!/usr/bin/env bash
# Builds the Linux x86-64 release tarball into dist/.
#   packaging/release/build-linux.sh <version>
# Needs the GUI build dependencies (see "make deps-debian" and friends).
set -euo pipefail
VERSION=${1:?usage: build-linux.sh <version>}
ARCH=${GOARCH:-amd64}
NAME="mu-archiver_${VERSION}_linux_${ARCH}"
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$ROOT"

STAGE="dist/stage/$NAME"
rm -rf "$STAGE"
mkdir -p "$STAGE/packaging"

export GOFLAGS="-trimpath -buildvcs=false" GOOS=linux GOARCH="$ARCH"
CGO_ENABLED=0 go build -ldflags "-s -w" -o "$STAGE/mu-dl" ./cmd/mu-dl
CGO_ENABLED=1 go build -ldflags "-s -w" -o "$STAGE/mu-archiver" ./cmd/mu-archiver

cp README.md LICENSE "$STAGE/"
cp packaging/install.sh "$STAGE/install.sh"
cp packaging/mu-archiver.desktop "$STAGE/packaging/"
cp -R packaging/icons "$STAGE/packaging/icons"
cp internal/assets/icon.png "$STAGE/packaging/mu-archiver.png"
chmod 755 "$STAGE/mu-dl" "$STAGE/mu-archiver" "$STAGE/install.sh"

# --owner/--group 0: no local user or group names in the tar headers
tar -C dist/stage --owner=0 --group=0 --numeric-owner -czf "dist/$NAME.tar.gz" "$NAME"
echo "built dist/$NAME.tar.gz"
