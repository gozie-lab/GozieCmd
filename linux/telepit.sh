#!/bin/sh
# Gozie Commander telepítése a saját felhasználónak (rendszergazda jog nem kell).
set -e
D="$(cd "$(dirname "$0")" && pwd)"
BIN="$HOME/.local/bin"
APPS="$HOME/.local/share/applications"
ICONS="$HOME/.local/share/icons/hicolor/256x256/apps"
mkdir -p "$BIN" "$APPS" "$ICONS"
install -m 755 "$D/goziecmd" "$BIN/goziecmd"
install -m 644 "$D/goziecmd.png" "$ICONS/goziecmd.png"
cat > "$APPS/hu.gozie.commander.desktop" <<DESK
[Desktop Entry]
Type=Application
Name=Gozie Commander
GenericName=Fájlkezelő
Comment=Kétpaneles fájlkezelő
Exec=$BIN/goziecmd
Icon=goziecmd
Terminal=false
Categories=System;FileManager;Utility;
Keywords=fájl;fájlkezelő;commander;file;manager;
StartupWMClass=@TITLE@
DESK
command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database "$APPS" >/dev/null 2>&1 || true
echo "Kész. A Gozie Commander megtalálható az alkalmazások között, vagy indítsd így: goziecmd"
