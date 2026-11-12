#!/bin/sh
# Preserved Linux desktop native source; fork integration is explicitly selected.
set -eu
repo=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
native="$repo/mobile/native/tinc"
. "$native/source-pin.env"
architecture=${1:-arm64}
case "$architecture" in arm64|amd64) architectures=$architecture;; all) architectures='arm64 amd64';; *) echo 'usage: build-tinc-linux.sh arm64|amd64|all' >&2;exit 2;;esac
work=${TINC_LINUX_BUILD_DIR:-/private/tmp/quantway-tinc-linux}
mkdir -p "$work"
upstream=${TINC_SOURCE_REPOSITORY:-/private/tmp/quantway-client-tinc/upstream}
if [ ! -d "$upstream" ];then git clone --depth 1 --branch "$TINC_UPSTREAM_TAG" "$TINC_UPSTREAM_URL" "$upstream";fi
test "$(git -C "$upstream" rev-parse HEAD)" = "$TINC_UPSTREAM_COMMIT"
context=$(mktemp -d "$work/native.XXXXXX")
mkdir "$context/source"
git -C "$upstream" archive "$TINC_UPSTREAM_COMMIT" > "$context/source.tar"
tar -xf "$context/source.tar" -C "$context/source"
rm "$context/source.tar"
cp "$native/socket_guard.c" "$native/socket_guard.h" "$context/source/src/"
for patch in "$repo"/deploy/tinc/patches/*.patch "$native"/patches/*.patch "$repo"/deploy/tinc-client/patches/*.patch;do git -C "$context/source" apply "$patch";done
python3 - "$context/source/configure.ac" "$TINC_CLIENT_VERSION" <<'PY'
from pathlib import Path
import sys
p=Path(sys.argv[1]);s=p.read_text();old="AC_INIT([tinc], m4_esyscmd_s((git describe || echo UNKNOWN) | sed 's/release-//'))"
if s.count(old)!=1:raise SystemExit('upstream AC_INIT changed')
p.write_text(s.replace(old,'AC_INIT([tinc], ['+sys.argv[2]+'])'))
PY
epoch=$(git -C "$upstream" show -s --format=%ct "$TINC_UPSTREAM_COMMIT")
output=${TINC_LINUX_OUTPUT_DIR:-"$repo/bin/quantway-tinc-linux"}
mkdir -p "$output"
for arch in $architectures;do
  bundle="$output/linux-$arch"
  mkdir -p "$bundle"
  docker build --platform "linux/$arch" -f "$native/Dockerfile.linux" --build-arg "SOURCE_DATE_EPOCH=$epoch" --target artifacts --output "type=local,dest=$bundle" "$context"
  (cd "$repo/mobile" && GOOS=linux GOARCH="$arch" CGO_ENABLED=0 GOCACHE="${GOCACHE:-/private/tmp/quantway-tinc-go-cache}" go build -trimpath -ldflags '-s -w' -o "$bundle/quantway-tinc" ./cmd/quantway-tinc)
  cp "$repo/mobile/clienttinc/README.md" "$bundle/README.md"
  {
    echo "client_tinc_version=$TINC_CLIENT_VERSION"
    echo "tinc_upstream_commit=$TINC_UPSTREAM_COMMIT"
    echo "tcp_relay_patch=1"
    echo "architecture=linux/$arch"
    shasum -a 256 "$native"/socket_guard.[ch] "$repo"/deploy/tinc-client/patches/*.patch
    shasum -a 256 "$bundle/quantway-tinc" "$bundle/tinc" "$bundle/tincd"
  } > "$bundle/BUILD-INFO.txt"
  (cd "$bundle" && shasum -a 256 quantway-tinc tinc tincd COPYING README.md BUILD-INFO.txt > SHA256SUMS)
  COPYFILE_DISABLE=1 tar --no-xattrs -czf "$output/quantway-tinc-linux-$arch.tar.gz" -C "$bundle" quantway-tinc tinc tincd COPYING README.md BUILD-INFO.txt SHA256SUMS
  echo "Linux pilot bundle: $output/quantway-tinc-linux-$arch.tar.gz"
done
(cd "$output" && shasum -a 256 quantway-tinc-linux-*.tar.gz > SHA256SUMS)
