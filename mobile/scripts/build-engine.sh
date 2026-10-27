#!/bin/sh
set -eu
mobile_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
: "${ANDROID_HOME:?Set ANDROID_HOME to the Android SDK directory}"
: "${JAVA_HOME:?Set JAVA_HOME to JDK 17}"
export PATH="$JAVA_HOME/bin:$(go env GOPATH)/bin:$PATH"
mobile_version=v0.0.0-20261011034528-197770444867
go install "golang.org/x/mobile/cmd/gomobile@$mobile_version"
go install "golang.org/x/mobile/cmd/gobind@$mobile_version"
cd "$mobile_dir"
gomobile init
# anet's Android interface cache uses linkname to Go net caches. The pinned
# Go 1.27 and x/net cache layouts match; recheck on toolchain upgrades.
gomobile bind -trimpath -ldflags="-s -w -checklinkname=0" -target=android/arm64,android/amd64 -androidapi=26 -o android/app/libs/quantway-engine.aar ./engine
