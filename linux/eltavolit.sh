#!/bin/sh
# Gozie Commander eltávolítása (a beállítások a ~/.config/GozieCommander mappában maradnak).
rm -f "$HOME/.local/bin/goziecmd" \
      "$HOME/.local/share/applications/hu.gozie.commander.desktop" \
      "$HOME/.local/share/icons/hicolor/256x256/apps/goziecmd.png"
echo "A Gozie Commander eltávolítva. A beállításaid: ~/.config/GozieCommander"
