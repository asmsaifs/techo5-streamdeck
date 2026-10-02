#!/bin/bash
# Builds dist/techo5-streamdeck_<version>_amd64.deb and dist/TECHO5-Stream-Deck-<version>-x86_64.AppImage.
# The AppImage step needs appimagetool on PATH (CI downloads it); without it only the .deb is made.
# Build dependencies (Debian/Ubuntu): libgtk-3-dev libwebkit2gtk-4.1-dev libx11-dev libxtst-dev
set -euo pipefail
cd "$(dirname "$0")/../.."
VERSION=${VERSION:-v0.0.0}
V=${VERSION#v}
ROOT=dist/linux/root
rm -rf dist/linux
mkdir -p $ROOT/usr/bin $ROOT/usr/share/applications $ROOT/usr/share/icons/hicolor/512x512/apps \
	$ROOT/lib/udev/rules.d $ROOT/DEBIAN

(cd frontend && npm ci && npm run build)
CGO_ENABLED=1 go build -trimpath -tags gtk3 -ldflags "-s -w -X main.version=$VERSION" \
	-o $ROOT/usr/bin/techo5-streamdeck .
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o $ROOT/usr/bin/deckcap-linux ./cmd/deckcap-linux
cp packaging/linux/techo5-streamdeck.desktop $ROOT/usr/share/applications/
cp packaging/icon.png $ROOT/usr/share/icons/hicolor/512x512/apps/techo5-streamdeck.png
cp packaging/linux/70-techo5-uinput.rules $ROOT/lib/udev/rules.d/

cat > $ROOT/DEBIAN/control <<CTL
Package: techo5-streamdeck
Version: $V
Section: utils
Priority: optional
Architecture: amd64
Depends: libgtk-3-0, libwebkit2gtk-4.1-0, libxtst6, pulseaudio-utils
Recommends: chromium | google-chrome-stable
Maintainer: ASM Saiful Islam Chowdhury <asmsaifs@gmail.com>
Description: Use an Echo Show running TECHO5 as a Stream Deck
 Touch buttons that run actions on this computer, and tiles that stream a
 website or an application window, with sound, to the Show.
CTL
mkdir -p dist
dpkg-deb --build --root-owner-group $ROOT "dist/techo5-streamdeck_${V}_amd64.deb"

if command -v appimagetool >/dev/null; then
	APPDIR=dist/linux/AppDir
	mkdir -p $APPDIR/usr
	cp -r $ROOT/usr/bin $ROOT/usr/share $APPDIR/usr/
	cp packaging/linux/techo5-streamdeck.desktop $APPDIR/
	cp packaging/icon.png $APPDIR/techo5-streamdeck.png
	printf '#!/bin/sh\nHERE="$(dirname "$(readlink -f "$0")")"\nexec "$HERE/usr/bin/techo5-streamdeck" "$@"\n' > $APPDIR/AppRun
	chmod +x $APPDIR/AppRun
	ARCH=x86_64 appimagetool $APPDIR "dist/TECHO5-Stream-Deck-$VERSION-x86_64.AppImage"
fi
