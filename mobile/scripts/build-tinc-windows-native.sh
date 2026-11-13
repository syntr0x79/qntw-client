#!/bin/sh
# Native Windows variant of build-tinc-windows.sh: same pinned upstream commit,
# same patches and Wintun device, built with MSYS2 (autotools/make) and the
# Windows-hosted LLVM-MinGW 20261020 instead of Docker.
#
# usage: build-tinc-windows-native.sh <repo> <upstream-clone> <work> <output> <llvm-mingw-dir>
set -eu
repo=$1 upstream=$2 work=$3 output=$4 llvm=$5
. "$repo/mobile/native/tinc/source-pin.env"
test "$(git -C "$upstream" rev-parse HEAD)" = "$TINC_UPSTREAM_COMMIT"
target=x86_64-w64-mingw32
export PATH="$llvm/bin:$PATH"
rm -rf "$work"
mkdir -p "$work" "$output"
git -C "$upstream" archive "$TINC_UPSTREAM_COMMIT" | tar -xf - -C "$work"
cd "$work"
git init -q .
for patch in "$repo"/deploy/tinc/patches/*.patch "$repo"/mobile/native/tinc/patches/*.patch "$repo"/deploy/tinc-client/patches/*.patch "$repo"/mobile/windows/native/*.patch; do
 git apply "$patch"
done
cp "$repo/mobile/windows/native/socket_guard_windows.c" src/socket_guard.c
cp "$repo/mobile/windows/native/socket_guard_windows.h" src/socket_guard.h
cp "$repo/mobile/windows/native/device_wintun.c" src/mingw/device.c
cp "$repo/mobile/windows/native/wintun.h" src/mingw/wintun.h
sed -i "s/AC_INIT(\[tinc\], m4_esyscmd_s((git describe || echo UNKNOWN) | sed 's\/release-\/\/'))/AC_INIT([tinc], [1.1pre18-quantway-windows2])/" configure.ac
grep -q '1.1pre18-quantway-windows2' configure.ac
sed -i 's/#define WINVER WindowsXP/#define WINVER 0x0a00\n#ifndef _WIN32_WINNT\n#define _WIN32_WINNT 0x0a00\n#endif/' src/have.h
sed -i 's/IP_MTU, &ip_mtu, &ip_mtu_len/IP_MTU, (char *)\&ip_mtu, \&ip_mtu_len/' src/net_packet.c
autoreconf -fsi
ac_cv_func_nanosleep=yes CC=$target-clang ./configure --host=$target --disable-legacy-protocol --disable-lzo --disable-zlib --disable-readline --disable-curses --disable-manpages --without-systemd CFLAGS="-O2 -DQUANTWAY_WINDOWS_CLIENT=1" LDFLAGS="-static" LIBS="-lwinpthread"
make -C src -j"$(nproc)" tincd.exe tinc.exe
$target-strip src/tincd.exe src/tinc.exe
cp src/tincd.exe src/tinc.exe COPYING "$output/"
echo "tinc built: $output"
