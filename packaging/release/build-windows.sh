#!/usr/bin/env bash
# Builds the Windows x86-64 release files into dist/stage/<name>/ (the release
# workflow zips that folder). Run from Git Bash / MSYS2 with a mingw-w64 gcc on
# PATH; cmd/mu-archiver/rsrc_windows_amd64.syso supplies the icon and version
# info (regenerate it with "make winres").
#   packaging/release/build-windows.sh <version>
set -euo pipefail
VERSION=${1:?usage: build-windows.sh <version>}
NAME="mu-archiver_${VERSION}_windows_amd64"
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$ROOT"

STAGE="dist/stage/$NAME"
rm -rf "$STAGE"
mkdir -p "$STAGE"

export GOFLAGS="-trimpath -buildvcs=false" GOOS=windows GOARCH=amd64
CGO_ENABLED=0 go build -ldflags "-s -w" -o "$STAGE/mu-dl.exe" ./cmd/mu-dl
# -H windowsgui: no console window behind the desktop app
CGO_ENABLED=1 go build -ldflags "-s -w -H windowsgui" -o "$STAGE/mu-archiver.exe" ./cmd/mu-archiver

cp README.md "$STAGE/README.md"
cp LICENSE "$STAGE/LICENSE.txt"
echo "staged $STAGE"
