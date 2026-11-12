#!/bin/sh
# Local, offline-capable Apple Silicon application bundle. No VPN is started.
set -eu
repo=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
if [ "${QUANTWAY_XRAY_BUILD_ENTRY:-}" != "$0" ]; then
  exec "$repo/mobile/scripts/with-xray-darwin.sh" env "QUANTWAY_XRAY_BUILD_ENTRY=$0" sh "$0" "$@"
fi
work=${QUANTWAY_MACOS_BUILD_DIR:-/private/tmp/quantway-macos-build}
output=${QUANTWAY_MACOS_OUTPUT_DIR:-"$repo/bin/quantway-macos-client"}
mkdir -p "$work" "$output"
export MACOSX_DEPLOYMENT_TARGET=13.0
export GOCACHE=${GOCACHE:-"$work/go-cache"}
export CLANG_MODULE_CACHE_PATH="$work/clang-modules"
export SWIFTPM_MODULECACHE_OVERRIDE="$work/swift-modules"
. "$repo/mobile/native/tinc/source-pin.env"
if [ ! -d "$repo/mobile/macos/build/QuantwayEngine.xcframework" ] || ! python3 - "$repo/mobile/macos/build/XRAY-BUILD-INFO.json" "$QUANTWAY_XRAY_SOURCE_FINGERPRINT" <<'PY'
import json, sys
from pathlib import Path
p = Path(sys.argv[1])
try:
    info = json.loads(p.read_text())
    assert info['source_fingerprint'] == sys.argv[2]
except (OSError, ValueError, KeyError, AssertionError):
    raise SystemExit(1)
PY
then
  "$repo/mobile/scripts/build-engine-macos.sh"
fi
test "$(uname -s)-$(uname -m)" = Darwin-arm64 || { echo 'This package requires a macOS ARM64 build host.' >&2; exit 2; }
native=${QUANTWAY_MACOS_TINC_DIR:-"$repo/bin/client-tinc/host-Darwin-arm64-fork"}
if [ ! -x "$native/tincd" ];then "$repo/mobile/scripts/build-client-tinc.sh" host;fi
# Package exactly the pinned, guarded, hook-free native build. Never rewrite C
# sources or reuse provenance from a different native executable.
python3 "$repo/mobile/scripts/verify-tinc-artifacts.py" "$native" --commit "$TINC_FORK_COMMIT" --target host-Darwin-arm64
go -C "$repo/mobile" build -trimpath -ldflags '-s -w -checklinkname=0' -o "$work/quantway-helper" ./linux/cmd/quantway
go -C "$repo/mobile" build -trimpath -ldflags '-s -w -checklinkname=0' -o "$work/quantway-vpn-helper" ./macos/cmd/quantway-vpn-helper
swift build --package-path "$repo/mobile/macos" --disable-sandbox --build-system native -c release --scratch-path "$work/swift"
binpath=$(swift build --package-path "$repo/mobile/macos" --disable-sandbox --build-system native -c release --scratch-path "$work/swift" --show-bin-path)
app="$output/Quantway.app"
if [ -e "$app" ];then mv "$app" "$output/Quantway-previous-$(date +%Y%m%d%H%M%S).app";fi
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources" "$app/Contents/Helpers"
cp "$binpath/Quantway" "$app/Contents/MacOS/Quantway"
cp -R "$binpath/QuantwayMac_QuantwayApp.bundle" "$app/Contents/Resources/"
cp "$work/quantway-helper" "$work/quantway-vpn-helper" "$native/tinc" "$native/tincd" "$app/Contents/Helpers/"
cp "$repo/mobile/macos/Info.plist" "$app/Contents/Info.plist"
mkdir -p "$app/Contents/Resources/Licenses"
cp "$repo/mobile/native/tinc/COPYING" "$app/Contents/Resources/Licenses/COPYING.tinc"
cp "$native/BUILD-INFO.txt" "$app/Contents/Resources/Licenses/tinc-fork.txt"
cp "$native/SHA256SUMS" "$app/Contents/Resources/Licenses/tinc-unsigned-SHA256SUMS"
cp "$native/tinc-source.tar.gz" "$app/Contents/Resources/Licenses/tinc-source.tar.gz"
mkdir -p "$app/Contents/Resources/Licenses/XrayDarwin"
cp "$QUANTWAY_XRAY_MANIFEST" "$app/Contents/Resources/Licenses/XrayDarwin/MANIFEST.json"
cp "$repo/mobile/macos/build/XRAY-BUILD-INFO.json" "$app/Contents/Resources/Licenses/XrayDarwin/FRAMEWORK-BUILD-INFO.json"
# Include the modified dependency source and patch used by both Go helpers and
# the Swift engine. The overlay itself contains build-local paths only.
find "$QUANTWAY_XRAY_OVERLAY_DIR" -maxdepth 1 -type f ! -name overlay.json ! -name MANIFEST.json -exec cp {} "$app/Contents/Resources/Licenses/XrayDarwin/" \;
for binary in "$app"/Contents/Helpers/*;do codesign --force --sign - "$binary";done
(cd "$app/Contents/Helpers" && shasum -a 256 tinc tincd quantway-helper quantway-vpn-helper) > "$app/Contents/Resources/Licenses/helpers-signed-SHA256SUMS"
codesign --force --sign - "$app"
codesign --verify --deep --strict "$app"
(cd "$output" && ditto -c -k --sequesterRsrc --keepParent Quantway.app Quantway-macOS-arm64.zip)
(cd "$output" && shasum -a 256 Quantway-macOS-arm64.zip > SHA256SUMS)
echo "$app"
