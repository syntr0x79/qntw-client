#!/bin/sh
# Pinned native Windows Wintun backend; independent from fleet/Linux artifacts.
set -eu
repo=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
arch=${1:-all}
case "$arch" in all) architectures='amd64 arm64';; amd64|arm64) architectures=$arch;; *) echo 'usage: build-tinc-windows.sh amd64|arm64|all' >&2; exit 2;; esac
work=${QUANTWAY_WINDOWS_BUILD_DIR:-/private/tmp/quantway-windows-build-20261101}
output=${TINC_WINDOWS_OUTPUT_DIR:-"$repo/bin/quantway-tinc-windows"}
mkdir -p "$work" "$output"
. "$repo/mobile/native/tinc/source-pin.env"
upstream=${TINC_SOURCE_REPOSITORY:-"$work/upstream"}
if [ ! -d "$upstream/.git" ]; then git clone --depth 1 --branch "$TINC_UPSTREAM_TAG" "$TINC_UPSTREAM_URL" "$upstream"; fi
test "$(git -C "$upstream" rev-parse HEAD)" = "$TINC_UPSTREAM_COMMIT"
case "$(uname -m)" in
 arm64|aarch64) llvm_host=aarch64; llvm_sha=ca16bb0f4f485876496fa46ad8d353c525eba57d0cf9e9e49ec1738fe4810151;;
 x86_64|amd64) llvm_host=x86_64; llvm_sha=55064b60b991a1e1e8090b8c2f84e82b22cfe5903d12c159d65064cf5e8ae335;;
 *) echo 'unsupported builder host' >&2; exit 2;;
esac
llvm_archive="$work/llvm-mingw-$llvm_host.tar.xz"
python3 - "$repo" "$llvm_archive" "$llvm_host" "$llvm_sha" <<'PY'
import pathlib,sys
sys.path.insert(0,str(pathlib.Path(sys.argv[1])/'mobile/windows'))
import runtime
runtime.fetch('https://github.com/mstorsjo/llvm-mingw/releases/download/20261020/llvm-mingw-20261020-ucrt-ubuntu-22.04-'+sys.argv[3]+'.tar.xz',pathlib.Path(sys.argv[2]),sys.argv[4])
PY
image=${QUANTWAY_WINDOWS_BUILDER_IMAGE:-quantway-windows-builder:llvm20261020}
if ! docker image inspect "$image" >/dev/null 2>&1; then
 context=$(mktemp -d "$work/docker.XXXXXX")
 cp "$llvm_archive" "$context/llvm-mingw.tar.xz"
 cp "$repo/mobile/windows/native/Dockerfile" "$context/Dockerfile"
 docker build --build-arg "LLVM_SHA256=$llvm_sha" -t "$image" "$context"
 rm -rf "$context"
fi
epoch=$(git -C "$upstream" show -s --format=%ct "$TINC_UPSTREAM_COMMIT")
source=$(mktemp -d "$work/native-source.XXXXXX")
git -C "$upstream" archive "$TINC_UPSTREAM_COMMIT" | tar -xf - -C "$source"
for patch in "$repo"/deploy/tinc/patches/*.patch "$repo"/mobile/native/tinc/patches/*.patch "$repo"/deploy/tinc-client/patches/*.patch; do git -C "$source" apply "$patch"; done
for patch in "$repo"/mobile/windows/native/*.patch; do git -C "$source" apply "$patch"; done
cp "$repo/mobile/windows/native/socket_guard_windows.c" "$source/src/socket_guard.c"
cp "$repo/mobile/windows/native/socket_guard_windows.h" "$source/src/socket_guard.h"
cp "$repo/mobile/windows/native/device_wintun.c" "$source/src/mingw/device.c"
cp "$repo/mobile/windows/native/wintun.h" "$source/src/mingw/wintun.h"
python3 - "$source/configure.ac" <<'PY'
import pathlib,sys
p=pathlib.Path(sys.argv[1]);s=p.read_text()
s=s.replace("AC_INIT([tinc], m4_esyscmd_s((git describe || echo UNKNOWN) | sed 's/release-//'))",'AC_INIT([tinc], [1.1pre18-quantway-windows2])')
p.write_text(s)
h=p.parent/'src/have.h'
h.write_text(h.read_text().replace('#define WINVER WindowsXP','#define WINVER 0x0a00\n#ifndef _WIN32_WINNT\n#define _WIN32_WINNT 0x0a00\n#endif'))
n=p.parent/'src/net_packet.c'
n.write_text(n.read_text().replace('IP_MTU, &ip_mtu, &ip_mtu_len','IP_MTU, (char *)&ip_mtu, &ip_mtu_len'))
PY
# Every changed native file and the full corresponding source travel with bundle.
COPYFILE_DISABLE=1 tar --no-xattrs -czf "$output/tinc-windows-corresponding-source.tar.gz" -C "$source" .
for architecture in $architectures; do
 case "$architecture" in amd64) target=x86_64-w64-mingw32;; arm64) target=aarch64-w64-mingw32;; esac
 build="$work/native-build-$architecture"
 mkdir -p "$build" "$output/$architecture"
 docker run --rm --network none --env "SOURCE_DATE_EPOCH=$epoch" -v "$source:/src:ro" -v "$build:/work" -v "$output/$architecture:/out" "$image" sh -eu -c '
  cp -a /src/. /work/
  autoreconf -fsi
  ac_cv_func_nanosleep=yes CC='"$target"'-clang ./configure --host='"$target"' --disable-legacy-protocol --disable-lzo --disable-zlib --disable-readline --disable-curses --disable-manpages --without-systemd CFLAGS="-O2 -DQUANTWAY_WINDOWS_CLIENT=1 -ffile-prefix-map=/work=/quantway-tinc-windows" LDFLAGS="-static" LIBS="-lwinpthread"
  make -C src -j4 tincd.exe tinc.exe
  '"$target"'-strip src/tincd.exe src/tinc.exe
  cp src/tincd.exe src/tinc.exe COPYING /out/
 '
 python3 - "$output/$architecture" "$repo/mobile/windows/native" "$TINC_UPSTREAM_COMMIT" "$architecture" "$llvm_sha" <<'PY'
import hashlib,json,pathlib,sys
out,native=map(pathlib.Path,sys.argv[1:3])
h=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
info={'upstream_commit':sys.argv[3],'architecture':'windows/'+sys.argv[4],'version':'1.1pre18-quantway-windows2','llvm_mingw_version':'20261020','llvm_archive_sha256':sys.argv[5],'native_sources_sha256':{p.name:h(p) for p in sorted(native.glob('*')) if p.is_file()},'binaries_sha256':{n:h(out/n) for n in ['tinc.exe','tincd.exe']}}
(out/'BUILD-INFO.json').write_text(json.dumps(info,indent=2)+'\n')
PY
done
