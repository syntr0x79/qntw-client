#!/bin/sh
set -eu
mobile_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
if [ "${QUANTWAY_XRAY_BUILD_ENTRY:-}" != "$0" ]; then
  exec "$mobile_dir/scripts/with-xray-darwin.sh" env "QUANTWAY_XRAY_BUILD_ENTRY=$0" sh "$0" "$@"
fi
mobile_version=v0.0.0-20261011034528-197770444867
export PATH="$(go env GOPATH)/bin:$PATH"
go install "golang.org/x/mobile/cmd/gomobile@$mobile_version"
go install "golang.org/x/mobile/cmd/gobind@$mobile_version"
cd "$mobile_dir"
mkdir -p macos/build
gomobile bind -macosversion=13.0 -trimpath -ldflags='-s -w -checklinkname=0' -target=macos/arm64,macos/amd64 -o macos/build/QuantwayEngine.xcframework ./engine
cp "$QUANTWAY_XRAY_MANIFEST" macos/build/XRAY-BUILD-INFO.json
