#!/bin/sh
# Gozie Commander fordítása: Windows (GozieCmd.exe) és Linux (goziecmd)
set -e
cd "$(dirname "$0")"
SDK="$PWD/linux-sdk"
export GOSUMDB=off GOFLAGS=-mod=mod
[ -d "$SDK" ] && export GOPROXY=direct
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-H windowsgui -s -w" -o GozieCmd.exe .
if [ -d "$SDK" ]; then # helyi fordítókörnyezet (fejlécek a linux-sdk mappában)
  GOOS=linux GOARCH=amd64 CGO_ENABLED=1 PKG_CONFIG_PATH="$SDK/pc" \
    CGO_CFLAGS="-I$SDK/inc -I$SDK/x11-headers -O2" CGO_LDFLAGS="-L$SDK/lib" \
    go build -trimpath -ldflags="-s -w" -o goziecmd .
else # szokásos Linux (pl. GitHub): libgl1-mesa-dev és xorg-dev csomag kell
  GOOS=linux GOARCH=amd64 CGO_ENABLED=1 go build -trimpath -ldflags="-s -w" -o goziecmd .
fi
