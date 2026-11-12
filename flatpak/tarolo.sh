#!/bin/sh
# A Flatpak-tároló weboldalának összeállítása (GitHub Actions használja).
# Használat: sh flatpak/tarolo.sh <site-mappa> <tároló-URL> <verzió>
set -e
SITE="$1"; URL="$2"; VER="$3"
KEY=$(base64 -w0 flatpak/gozie-flatpak.gpg)

cat > "$SITE/gozie.flatpakrepo" <<EOF
[Flatpak Repo]
Title=Gozie Commander
Url=$URL/repo/
Homepage=https://cmd.gozie.hu/
Comment=Gozie Commander – kétpaneles fájlkezelő / two-panel file manager
Icon=$URL/ikon.png
GPGKey=$KEY
EOF

cat > "$SITE/hu.gozie.commander.flatpakref" <<EOF
[Flatpak Ref]
Name=hu.gozie.commander
Branch=stable
Title=Gozie Commander
Url=$URL/repo/
SuggestRemoteName=gozie
RuntimeRepo=https://dl.flathub.org/repo/flathub.flatpakrepo
IsRuntime=false
Homepage=https://cmd.gozie.hu/
Icon=$URL/ikon.png
GPGKey=$KEY
EOF

cp goziecmd.png "$SITE/ikon.png"
sed -e "s#@URL@#$URL#g" -e "s#@VER@#$VER#g" flatpak/oldal/index.html > "$SITE/index.html"
