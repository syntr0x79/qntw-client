#!/bin/sh
# Standalone stage-0 build. Does not modify the app, engine or fleet artifacts.
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
native_dir="$repo_dir/mobile/native/tinc"
. "$native_dir/source-pin.env"

platform=${1:-android}
abi=${2:-arm64-v8a}
case "$platform" in
  host) target_name="host-$(uname -s)-$(uname -m)" ;;
  android) target_name="android-$abi" ;;
  *) echo "Usage: $0 [host | android [arm64-v8a|x86_64|armeabi-v7a|x86]]" >&2; exit 2 ;;
esac
# The isolated stage-0 stand and Android packet-FD harness reproduced the
# unpatched TCP transit failure and passed with this client-only relay fix.
relay_patch=${TINC_CLIENT_TCP_RELAY_PATCH:-1}
case "$relay_patch" in
  0) ;;
  1) target_name="$target_name-tcp-relay" ;;
  *) echo "TINC_CLIENT_TCP_RELAY_PATCH must be 0 or 1" >&2; exit 2 ;;
esac

work_root=${TINC_CLIENT_BUILD_DIR:-/private/tmp/quantway-client-tinc}
output_root=${TINC_CLIENT_OUTPUT_DIR:-"$repo_dir/bin/client-tinc"}
mkdir -p "$work_root" "$output_root"
work_root=$(CDPATH= cd -- "$work_root" && pwd)
output_root=$(CDPATH= cd -- "$output_root" && pwd)

# A caller may supply a local Git repository. Archive the pinned commit rather
# than copying its working tree: unrelated or old uncommitted patches stay out.
upstream=${TINC_SOURCE_REPOSITORY:-"$work_root/upstream"}
if [ ! -d "$upstream" ]; then
  git clone --no-checkout --depth 1 --branch "$TINC_UPSTREAM_TAG" "$TINC_UPSTREAM_URL" "$upstream"
fi
actual=$(git -C "$upstream" rev-parse "$TINC_UPSTREAM_COMMIT^{commit}")
test "$actual" = "$TINC_UPSTREAM_COMMIT"
export SOURCE_DATE_EPOCH
SOURCE_DATE_EPOCH=$(git -C "$upstream" show -s --format=%ct "$TINC_UPSTREAM_COMMIT")

work_dir=$(mktemp -d "$work_root/$target_name.XXXXXX")
source_dir="$work_dir/source"
build_dir="$work_dir/build"
mkdir -p "$source_dir" "$build_dir"
git -C "$upstream" archive "$TINC_UPSTREAM_COMMIT" > "$work_dir/source.tar"
tar -xf "$work_dir/source.tar" -C "$source_dir"
rm "$work_dir/source.tar"
cp "$native_dir/socket_guard.c" "$native_dir/socket_guard.h" "$source_dir/src/"

manifest="$work_dir/BUILD-INFO.txt"
{
  echo "version=$TINC_CLIENT_VERSION"
  echo "upstream_commit=$TINC_UPSTREAM_COMMIT"
  echo "target=$target_name"
  echo "source_date_epoch=$SOURCE_DATE_EPOCH"
  echo "experimental_tcp_relay_patch=$relay_patch"
  echo "source_work_directory=$work_dir"
} > "$manifest"
for patch_file in "$repo_dir"/deploy/tinc/patches/*.patch "$native_dir"/patches/*.patch; do
  git -C "$source_dir" apply --check "$patch_file"
  git -C "$source_dir" apply "$patch_file"
  shasum -a 256 "$patch_file" >> "$manifest"
done
if [ "$relay_patch" = 1 ]; then
  patch_file="$repo_dir/deploy/tinc-client/patches/0001-relay-existing-sptps-records-over-tcp.patch"
  git -C "$source_dir" apply --check "$patch_file"
  git -C "$source_dir" apply "$patch_file"
  shasum -a 256 "$patch_file" >> "$manifest"
fi
shasum -a 256 "$native_dir/socket_guard.c" "$native_dir/socket_guard.h" >> "$manifest"

# configure.ac obtains its version from Git. The archive deliberately has no
# .git directory; give that one field a fixed version before regeneration.
python3 - "$source_dir/configure.ac" "$TINC_CLIENT_VERSION" <<'PY'
import pathlib, sys
p = pathlib.Path(sys.argv[1])
s = p.read_text()
old = "AC_INIT([tinc], m4_esyscmd_s((git describe || echo UNKNOWN) | sed 's/release-//'))"
if s.count(old) != 1:
    raise SystemExit("upstream AC_INIT changed; inspect the pinned source")
p.write_text(s.replace(old, "AC_INIT([tinc], [" + sys.argv[2] + "])"))
PY
(cd "$source_dir" && autoreconf -fsi)

set -- --disable-legacy-protocol --disable-lzo --disable-zlib \
  --disable-readline --disable-curses --disable-manpages --without-systemd \
  --sysconfdir=/etc --localstatedir=/var
if [ "$platform" = android ]; then
  : "${ANDROID_NDK_HOME:?Set ANDROID_NDK_HOME to an installed Android NDK}"
  ndk_revision=$(sed -n 's/^Pkg.Revision = //p' "$ANDROID_NDK_HOME/source.properties")
  if [ "$ndk_revision" != "$TINC_ANDROID_NDK_REVISION" ]; then
    echo "Expected Android NDK $TINC_ANDROID_NDK_REVISION, got $ndk_revision" >&2
    exit 2
  fi
  # The official macOS NDK uses this directory name on Apple Silicon too.
  case "$(uname -s)" in
    Darwin) ndk_host=darwin-x86_64 ;;
    Linux) ndk_host=linux-x86_64 ;;
    *) echo "Unsupported NDK build host" >&2; exit 2 ;;
  esac
  toolchain="$ANDROID_NDK_HOME/toolchains/llvm/prebuilt/$ndk_host/bin"
  case "$abi" in
    arm64-v8a) compiler_target=aarch64-linux-android; configure_target=$compiler_target ;;
    x86_64) compiler_target=x86_64-linux-android; configure_target=$compiler_target ;;
    armeabi-v7a) compiler_target=armv7a-linux-androideabi; configure_target=arm-linux-androideabi ;;
    x86) compiler_target=i686-linux-android; configure_target=$compiler_target ;;
    *) echo "Unsupported Android ABI: $abi" >&2; exit 2 ;;
  esac
  export CC="$toolchain/${compiler_target}${TINC_ANDROID_API}-clang"
  export AR="$toolchain/llvm-ar"
  export RANLIB="$toolchain/llvm-ranlib"
  export STRIP="$toolchain/llvm-strip"
  test -x "$CC"
  set -- "$@" "--host=$configure_target"
  echo "ndk_revision=$ndk_revision" >> "$manifest"
  echo "android_api=$TINC_ANDROID_API" >> "$manifest"
fi
export CFLAGS="${CFLAGS:--O2 -g} -ffile-prefix-map=$work_dir=/quantway-client-tinc -fdebug-prefix-map=$work_dir=/quantway-client-tinc"
{
  echo "compiler=${CC:-cc}"
  echo "cflags=$CFLAGS"
  autoreconf --version | head -1
  automake --version | head -1
} >> "$manifest"
(
  cd "$build_dir"
  "$source_dir/configure" "$@"
  make -C src -j "${TINC_BUILD_JOBS:-4}" tincd tinc
)

destination="$output_root/$target_name"
mkdir -p "$destination"
cp "$build_dir/src/tincd" "$destination/tincd"
cp "$build_dir/src/tinc" "$destination/tinc"
cp "$native_dir/COPYING" "$destination/COPYING"
cp "$manifest" "$destination/BUILD-INFO.txt"
shasum -a 256 "$destination/tincd" "$destination/tinc" >> "$destination/BUILD-INFO.txt"
file "$destination/tincd" "$destination/tinc"
echo "Client tinc spike artifacts: $destination"
