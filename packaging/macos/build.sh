#!/bin/bash
# Builds dist/TECHO5-Stream-Deck-<version>.dmg: a universal .app with the capture helper next to
# the program, signed and notarized when the environment has the credentials.
#
#   VERSION            v1.2.3 (default: dev)
#   CODESIGN_IDENTITY  "Developer ID Application: …"; unset: ad-hoc signature (runs on this Mac only)
#   NOTARY_PROFILE     a notarytool keychain profile; unset: not notarized
set -euo pipefail
cd "$(dirname "$0")/../.."
VERSION=${VERSION:-dev}
APP="dist/TECHO5 Stream Deck.app"
rm -rf dist/mac "$APP"
mkdir -p dist/mac "$APP/Contents/MacOS" "$APP/Contents/Resources"

(cd frontend && npm ci && npm run build)

for arch in arm64 amd64; do
	CGO_ENABLED=1 GOARCH=$arch go build -trimpath -ldflags "-s -w -X main.version=$VERSION" \
		-o "dist/mac/app-$arch" .
	swift build -c release --package-path helpers/mac --arch "$([ $arch = amd64 ] && echo x86_64 || echo arm64)"
	cp "helpers/mac/.build/$([ $arch = amd64 ] && echo x86_64 || echo arm64)-apple-macosx/release/deckcap-mac" "dist/mac/helper-$arch"
done
lipo -create dist/mac/app-arm64 dist/mac/app-amd64 -output "$APP/Contents/MacOS/techo5-streamdeck"
lipo -create dist/mac/helper-arm64 dist/mac/helper-amd64 -output "$APP/Contents/MacOS/deckcap-mac"

sed "s/@VERSION@/${VERSION#v}/" packaging/macos/Info.plist > "$APP/Contents/Info.plist"
ICONSET=dist/mac/icon.iconset
mkdir -p $ICONSET
for s in 16 32 64 128 256 512; do
	sips -z $s $s packaging/icon.png --out "$ICONSET/icon_${s}x${s}.png" >/dev/null
	sips -z $((s*2)) $((s*2)) packaging/icon.png --out "$ICONSET/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns $ICONSET -o "$APP/Contents/Resources/icon.icns"

IDENT=${CODESIGN_IDENTITY:--}
OPTS=(--force --timestamp --options runtime --entitlements packaging/macos/entitlements.plist --sign "$IDENT")
[ "$IDENT" = "-" ] && OPTS=(--force --sign -)
# Inside out: the helper first, then the bundle.
codesign "${OPTS[@]}" "$APP/Contents/MacOS/deckcap-mac"
codesign "${OPTS[@]}" "$APP"
codesign --verify --strict --verbose=2 "$APP"

DMG="dist/TECHO5-Stream-Deck-$VERSION.dmg"
rm -f "$DMG"
hdiutil create -volname "TECHO5 Stream Deck" -srcfolder "$APP" -ov -format UDZO "$DMG"
if [ "$IDENT" != "-" ]; then codesign --force --sign "$IDENT" "$DMG"; fi
if [ -n "${NOTARY_PROFILE:-}" ]; then
	xcrun notarytool submit "$DMG" --keychain-profile "$NOTARY_PROFILE" --wait
	xcrun stapler staple "$DMG"
fi
echo "$DMG"
