#!/usr/bin/env bash
# Builds the universal (Apple silicon + Intel) macOS release zip into dist/:
# "MU Archiver.app" plus the mu-dl command-line tool. Runs on macOS with the
# Xcode command-line tools (clang, lipo, sips, iconutil, codesign, ditto).
#   packaging/release/build-macos.sh <version>
set -euo pipefail
VERSION=${1:?usage: build-macos.sh <version>}
NAME="mu-archiver_${VERSION}_macos_universal"
APP="MU Archiver.app"
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$ROOT"

STAGE="dist/stage/$NAME"
BUILD="dist/build/macos"
rm -rf "$STAGE" "$BUILD"
mkdir -p "$STAGE" "$BUILD"

export GOFLAGS="-trimpath -buildvcs=false" GOOS=darwin MACOSX_DEPLOYMENT_TARGET=11.0
for arch in arm64 amd64; do
  CGO_ENABLED=0 GOARCH=$arch go build -ldflags "-s -w" -o "$BUILD/mu-dl-$arch" ./cmd/mu-dl
  CGO_ENABLED=1 GOARCH=$arch go build -ldflags "-s -w" -o "$BUILD/mu-archiver-$arch" ./cmd/mu-archiver
done
lipo -create -output "$BUILD/mu-dl" "$BUILD/mu-dl-arm64" "$BUILD/mu-dl-amd64"
lipo -create -output "$BUILD/mu-archiver" "$BUILD/mu-archiver-arm64" "$BUILD/mu-archiver-amd64"

# Icon: .icns from the 512 px master (no 1024 px master, so no 512@2x)
ICONSET="$BUILD/AppIcon.iconset"
mkdir -p "$ICONSET"
for s in 16 32 128 256 512; do
  sips -z "$s" "$s" internal/assets/icon.png --out "$ICONSET/icon_${s}x${s}.png" >/dev/null
  if [ "$s" -lt 512 ]; then
    sips -z $((s * 2)) $((s * 2)) internal/assets/icon.png --out "$ICONSET/icon_${s}x${s}@2x.png" >/dev/null
  fi
done
iconutil -c icns "$ICONSET" -o "$BUILD/AppIcon.icns"

# CFBundleVersion must be digits and dots; dev builds carry a suffix
BUNDLE_VERSION=$(printf '%s' "$VERSION" | grep -oE '^[0-9]+(\.[0-9]+)*' || echo 0)
C="$STAGE/$APP/Contents"
mkdir -p "$C/MacOS" "$C/Resources"
cp "$BUILD/mu-archiver" "$C/MacOS/mu-archiver"
cp "$BUILD/AppIcon.icns" "$C/Resources/AppIcon.icns"
cat > "$C/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleDevelopmentRegion</key>
	<string>en</string>
	<key>CFBundleDisplayName</key>
	<string>MU Archiver</string>
	<key>CFBundleExecutable</key>
	<string>mu-archiver</string>
	<key>CFBundleIconFile</key>
	<string>AppIcon</string>
	<key>CFBundleIdentifier</key>
	<string>org.muarchiver.MUArchiver</string>
	<key>CFBundleInfoDictionaryVersion</key>
	<string>6.0</string>
	<key>CFBundleName</key>
	<string>MU Archiver</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>${VERSION}</string>
	<key>CFBundleVersion</key>
	<string>${BUNDLE_VERSION}</string>
	<key>LSApplicationCategoryType</key>
	<string>public.app-category.utilities</string>
	<key>LSMinimumSystemVersion</key>
	<string>11.0</string>
	<key>NSHighResolutionCapable</key>
	<true/>
	<key>NSHumanReadableCopyright</key>
	<string>CC0 1.0 Universal</string>
</dict>
</plist>
PLIST

# Ad-hoc signatures: required for arm64 binaries to run at all. Not notarised.
codesign --force --deep --sign - "$STAGE/$APP"
cp "$BUILD/mu-dl" "$STAGE/mu-dl"
codesign --force --sign - "$STAGE/mu-dl"
cp README.md LICENSE "$STAGE/"

# ditto keeps the bundle structure, permissions and signatures intact
(cd dist/stage && ditto -c -k --keepParent "$NAME" "../$NAME.zip")
echo "built dist/$NAME.zip"
