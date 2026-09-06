#!/usr/bin/env bash
# Installs MU Archiver for the current user from an unpacked release tarball:
# binaries into ~/.local/bin, the launcher into ~/.local/share/applications and
# the icon into the hicolor theme (the same layout as "make install").
#   ./install.sh               install (PREFIX=... to change ~/.local)
#   ./install.sh --uninstall   remove everything again
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
PREFIX=${PREFIX:-$HOME/.local}
BINDIR=$PREFIX/bin
APPDIR=$PREFIX/share/applications
ICONDIR=$PREFIX/share/icons/hicolor
APPID=org.muarchiver.MUArchiver
SIZES="32 48 64 128 256 512"

refresh() {
  update-desktop-database "$APPDIR" 2>/dev/null || true
  gtk-update-icon-cache -q -t "$ICONDIR" 2>/dev/null || true
  xdg-icon-resource forceupdate --theme hicolor 2>/dev/null || true
  kbuildsycoca6 --noincremental >/dev/null 2>&1 || kbuildsycoca5 --noincremental >/dev/null 2>&1 || true
}

if [ "${1:-}" = "--uninstall" ]; then
  rm -f "$BINDIR/mu-dl" "$BINDIR/mu-archiver" "$APPDIR/$APPID.desktop" \
        "$PREFIX/share/pixmaps/mu-archiver.png" \
        "${XDG_CONFIG_HOME:-$HOME/.config}/autostart/mu-archiver.desktop"
  for s in $SIZES; do rm -f "$ICONDIR/${s}x${s}/apps/mu-archiver.png"; done
  refresh
  echo "Removed MU Archiver from $PREFIX (settings in ~/.config/mu-dl and the archive itself were left alone)."
  exit 0
fi

for f in mu-dl mu-archiver packaging/mu-archiver.desktop packaging/mu-archiver.png; do
  [ -e "$HERE/$f" ] || { echo "install.sh: $f missing next to this script; unpack the full release tarball first." >&2; exit 1; }
done

install -d "$BINDIR" "$APPDIR" "$PREFIX/share/pixmaps"
install -m 0755 "$HERE/mu-dl" "$BINDIR/mu-dl"
install -m 0755 "$HERE/mu-archiver" "$BINDIR/mu-archiver"
for s in $SIZES; do
  install -d "$ICONDIR/${s}x${s}/apps"
  install -m 0644 "$HERE/packaging/icons/hicolor/${s}x${s}/apps/mu-archiver.png" "$ICONDIR/${s}x${s}/apps/mu-archiver.png"
done
install -m 0644 "$HERE/packaging/mu-archiver.png" "$PREFIX/share/pixmaps/mu-archiver.png"
sed "s#^Exec=mu-archiver#Exec=$BINDIR/mu-archiver#; s#^TryExec=mu-archiver#TryExec=$BINDIR/mu-archiver#; s#^Icon=.*#Icon=$ICONDIR/512x512/apps/mu-archiver.png#" \
  "$HERE/packaging/mu-archiver.desktop" > "$APPDIR/$APPID.desktop"
refresh

echo "Installed. Launch 'MU Archiver' from the application menu, or run $BINDIR/mu-archiver and $BINDIR/mu-dl."
case ":$PATH:" in
  *":$BINDIR:"*) ;;
  *) echo "Note: $BINDIR is not on your PATH; add it to use the commands by name." ;;
esac
