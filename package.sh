#!/bin/sh
# Kiadási csomagok: GozieCmd-<ver>-windows.zip és GozieCmd-<ver>-linux-x86_64.tar.gz
set -e
cd "$(dirname "$0")"
VER=$(sed -n 's/^const appVersion = "\(.*\)"/\1/p' version.go)
sh ./build.sh # (Windowson át feltöltve a futtathatóság elveszhet)
OUT="${1:-..}"
rm -rf /tmp/gzpkg && mkdir -p /tmp/gzpkg/win/GozieCmd /tmp/gzpkg/lx/GozieCmd
cp GozieCmd.exe /tmp/gzpkg/win/GozieCmd/
for f in LICENSE THIRD_PARTY_LICENSES.txt; do sed 's/$/\r/' $f > /tmp/gzpkg/win/GozieCmd/$f.txt; done
mv /tmp/gzpkg/win/GozieCmd/THIRD_PARTY_LICENSES.txt.txt /tmp/gzpkg/win/GozieCmd/THIRD_PARTY_LICENSES.txt
sed "s/^Gozie Commander [0-9.]* /Gozie Commander $VER /; s/\$/\r/" OLVASS.txt > /tmp/gzpkg/win/GozieCmd/OLVASS.txt
(cd /tmp/gzpkg/win && rm -f "$OUT/GozieCmd-$VER-windows.zip" && zip -qr "$(cd "$OLDPWD" && cd "$OUT" && pwd)/GozieCmd-$VER-windows.zip" GozieCmd)
cp goziecmd goziecmd.png linux/eltavolit.sh LICENSE THIRD_PARTY_LICENSES.txt /tmp/gzpkg/lx/GozieCmd/
sed "s/@TITLE@/Gozie Commander $VER/" linux/telepit.sh > /tmp/gzpkg/lx/GozieCmd/telepit.sh
chmod +x /tmp/gzpkg/lx/GozieCmd/telepit.sh /tmp/gzpkg/lx/GozieCmd/eltavolit.sh /tmp/gzpkg/lx/GozieCmd/goziecmd
sed "s/^Gozie Commander [0-9.]* /Gozie Commander $VER /" linux/OLVASS.txt > /tmp/gzpkg/lx/GozieCmd/OLVASS.txt
tar -C /tmp/gzpkg/lx -czf "$OUT/GozieCmd-$VER-linux-x86_64.tar.gz" GozieCmd
ls -la "$OUT/GozieCmd-$VER-windows.zip" "$OUT/GozieCmd-$VER-linux-x86_64.tar.gz"
