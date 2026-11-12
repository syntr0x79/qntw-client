#!/bin/sh
# Standalone Linux client-layer bundle, independent from mobile/linux UI.
set -eu
repo=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
native="$repo/mobile/native/tinc"
. "$native/source-pin.env"
architecture=${1:-arm64}
case "$architecture" in arm64|amd64) architectures=$architecture;; all) architectures='arm64 amd64';; *) echo 'usage: build-tinc-linux.sh arm64|amd64|all' >&2;exit 2;;esac
work=${TINC_LINUX_BUILD_DIR:-/private/tmp/quantway-tinc-linux-fork}
output=${TINC_LINUX_OUTPUT_DIR:-"$repo/bin/quantway-tinc-linux-fork"}
mkdir -p "$work" "$output"
work=$(CDPATH= cd -- "$work" && pwd)
output=$(CDPATH= cd -- "$output" && pwd)
context=$(mktemp -d "$work/native.XXXXXX")
"$repo/mobile/scripts/prepare-tinc-fork.sh" "$context"
epoch=$(sed -n 's/^source_date_epoch=//p' "$context/BUILD-INFO.txt")
for arch in $architectures;do
  staging=$(mktemp -d "$output/.linux-$arch.XXXXXX")
  docker build --platform "linux/$arch" -f "$native/Dockerfile.linux-fork" \
    --build-arg "SOURCE_DATE_EPOCH=$epoch" --build-arg "TINC_MESON_VERSION=$TINC_MESON_VERSION" \
    --build-arg "TINC_LINUX_NINJA_PACKAGE=$TINC_LINUX_NINJA_PACKAGE" \
    --target artifacts --output "type=local,dest=$staging" "$context"
  (cd "$repo/mobile" && GOOS=linux GOARCH="$arch" CGO_ENABLED=0 GOCACHE="${GOCACHE:-/private/tmp/quantway-tinc-go-cache}" go build -trimpath -ldflags '-s -w' -o "$staging/quantway-tinc" ./cmd/quantway-tinc)
  cp "$repo/mobile/clienttinc/README.md" "$staging/README.md"
  {
    echo "target=linux-$arch"
    echo "architecture=linux/$arch"
    (cd "$staging" && shasum -a 256 quantway-tinc tinc tincd tinc-source.tar.gz)
  } >> "$staging/BUILD-INFO.txt"
  (cd "$staging" && shasum -a 256 quantway-tinc tinc tincd COPYING README.md BUILD-INFO.txt tinc-source.tar.gz > SHA256SUMS)
  bundle="$output/linux-$arch"
  if [ -e "$bundle" ];then mv "$bundle" "$bundle-previous-$(date +%Y%m%d%H%M%S)-$$";fi
  mv "$staging" "$bundle"
  COPYFILE_DISABLE=1 tar --no-xattrs -czf "$output/.quantway-tinc-linux-$arch.tar.gz.tmp" -C "$bundle" quantway-tinc tinc tincd COPYING README.md BUILD-INFO.txt SHA256SUMS tinc-source.tar.gz
  mv "$output/.quantway-tinc-linux-$arch.tar.gz.tmp" "$output/quantway-tinc-linux-$arch.tar.gz"
  echo "Linux client-layer bundle: $output/quantway-tinc-linux-$arch.tar.gz"
done
(cd "$output" && shasum -a 256 quantway-tinc-linux-*.tar.gz > SHA256SUMS.tmp && mv SHA256SUMS.tmp SHA256SUMS)
