#!/bin/sh
# Client-layer native fork. The Android spike retains its legacy build for now.
set -eu
repo=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
. "$repo/mobile/native/tinc/source-pin.env"
platform=${1:-android}
if [ "$platform" = android ]; then
  exec "$repo/mobile/scripts/build-client-tinc-legacy.sh" "$@"
fi
if [ "$platform" != host ]; then
  echo 'usage: build-client-tinc.sh host | android [abi]' >&2; exit 2
fi
meson=${TINC_MESON_BIN:-meson}
ninja=${TINC_NINJA_BIN:-ninja}
meson_version=$("$meson" --version)
ninja_version=$("$ninja" --version)
# The pinned PyPI Ninja wheel appends its Kitware patch suffix to --version.
case "$ninja_version" in "$TINC_NINJA_VERSION"|"$TINC_NINJA_VERSION.git.kitware.jobserver-pipe-1") ;; *) echo 'Unexpected Ninja version' >&2; exit 2;; esac
if [ "$meson_version" != "$TINC_MESON_VERSION" ]; then
  echo "Expected Meson $TINC_MESON_VERSION and Ninja $TINC_NINJA_VERSION (set TINC_MESON_BIN/TINC_NINJA_BIN or PATH)" >&2; exit 2
fi
# Meson discovers Ninja itself, so make the explicitly selected executable visible.
export NINJA="$ninja"
target="host-$(uname -s)-$(uname -m)"
work_root=${TINC_CLIENT_BUILD_DIR:-/private/tmp/quantway-client-tinc-fork}
output_root=${TINC_CLIENT_OUTPUT_DIR:-"$repo/bin/client-tinc"}
mkdir -p "$work_root" "$output_root"
work_root=$(CDPATH= cd -- "$work_root" && pwd)
output_root=$(CDPATH= cd -- "$output_root" && pwd)
work=$(mktemp -d "$work_root/$target.XXXXXX")
"$repo/mobile/scripts/prepare-tinc-fork.sh" "$work"
source_dir="$work/source"
build_dir="$work/build"
export SOURCE_DATE_EPOCH
SOURCE_DATE_EPOCH=$(sed -n 's/^source_date_epoch=//p' "$work/BUILD-INFO.txt")
export CFLAGS="${CFLAGS:--O2 -g} -ffile-prefix-map=$work=/quantway-client-tinc -fdebug-prefix-map=$work=/quantway-client-tinc"
"$meson" setup "$build_dir" "$source_dir" --buildtype=release --wrap-mode=nofallback \
  -Dcrypto=nolegacy -Dsocket_guard=true -Dclient_hooks=false -Dudp_batch_size=1 -Dudp_offload=false \
  -Ddocs=disabled -Dsystemd=disabled -Dlzo=disabled -Dlz4=disabled -Dzlib=disabled \
  -Dreadline=disabled -Dcurses=disabled -Dvde=disabled -Dminiupnpc=disabled \
  -Dtunemu=disabled -Dvmnet=disabled
"$meson" compile -C "$build_dir" -j "${TINC_BUILD_JOBS:-4}" src/tinc:executable src/tincd:executable
# A fresh output directory prevents a failed build from replacing prior artifacts.
destination="$output_root/$target-fork"
staging=$(mktemp -d "$output_root/.$target-fork.XXXXXX")
cp "$build_dir/src/tinc" "$build_dir/src/tincd" "$source_dir/COPYING" "$work/tinc-source.tar.gz" "$staging/"
cp "$work/BUILD-INFO.txt" "$staging/BUILD-INFO.txt"
{
  echo 'profile=supervised-client'
  echo "target=$target"
  echo 'socket_guard=true'
  echo 'client_hooks=false'
  echo 'crypto=nolegacy'
  echo 'udp_batch_size=1'
  echo 'udp_offload=false'
  echo "meson_version=$TINC_MESON_VERSION"
  echo "ninja_version=$ninja_version"
  echo "build_directory=$build_dir"
  echo "compiler=${CC:-cc}"
  echo "cflags=$CFLAGS"
  "${CC:-cc}" --version | head -1
  (cd "$staging" && shasum -a 256 tinc tincd tinc-source.tar.gz)
} >> "$staging/BUILD-INFO.txt"
(cd "$staging" && shasum -a 256 tinc tincd COPYING BUILD-INFO.txt tinc-source.tar.gz > SHA256SUMS)
if [ -e "$destination" ]; then mv "$destination" "$destination-previous-$(date +%Y%m%d%H%M%S)-$$"; fi
mv "$staging" "$destination"
file "$destination/tinc" "$destination/tincd"
echo "Client fork artifacts: $destination"
