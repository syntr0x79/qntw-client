#!/bin/sh
set -eu

spike_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_dir=$(CDPATH= cd -- "$spike_dir/../../.." && pwd)
spike_out=${CLIENT_TINC_ANDROID_SPIKE_OUT:-/private/tmp/clienttinc-android-spike}
sdk_dir=${ANDROID_HOME:-$HOME/Library/Android/sdk}
ndk_dir=${ANDROID_NDK_HOME:-$sdk_dir/ndk/28.0.13004108}
native_test=${CLIENT_TINC_ANDROID_TEST:-$spike_out/clienttinc-android.test}
native_dir=${CLIENT_TINC_ANDROID_BIN:-$repo_dir/bin/client-tinc/android-arm64-v8a-tcp-relay}
adb_bin="$sdk_dir/platform-tools/adb"
serial=${ANDROID_SERIAL:-emulator-5554}
java_dir=${JAVA_HOME:-/opt/homebrew/opt/openjdk@17/libexec/openjdk.jdk/Contents/Home}

mkdir -p "$spike_out/native/arm64-v8a"
if [ -z "${CLIENT_TINC_ANDROID_TEST:-}" ]; then
    (
        cd "$repo_dir/mobile"
        CC="$ndk_dir/toolchains/llvm/prebuilt/darwin-x86_64/bin/aarch64-linux-android26-clang" \
            CGO_ENABLED=1 GOOS=android GOARCH=arm64 go test -c -buildmode=pie \
            -o "$native_test" ./tincbridge
    )
fi
cp "$native_test" "$spike_out/native/arm64-v8a/libtinctest.so"
cp "$native_dir/tinc" "$spike_out/native/arm64-v8a/libtincctl.so"
cp "$native_dir/tincd" "$spike_out/native/arm64-v8a/libtincd.so"
"$ndk_dir/toolchains/llvm/prebuilt/darwin-x86_64/bin/aarch64-linux-android26-clang" \
    -O2 -fPIE -pie "$spike_dir/fixture.c" -o "$spike_out/native/arm64-v8a/libfixture.so"

JAVA_HOME="$java_dir" ANDROID_HOME="$sdk_dir" "$repo_dir/mobile/android/gradlew" \
    --offline --no-daemon -p "$spike_dir" --project-cache-dir "$spike_out/gradle-cache" \
    -PspikeBuildDir="$spike_out/build" -PspikeNativeDir="$spike_out/native" assembleDebug
"$adb_bin" -s "$serial" install -r "$spike_out/build/outputs/apk/debug/TincBridgeSpike-debug.apk"
if [ "${CLIENT_TINC_ANDROID_VPN_GUARD:-0}" = "1" ]; then
    "$adb_bin" -s "$serial" shell appops set link.quantway.tincspike ACTIVATE_VPN allow
fi
cleanup() {
    "$adb_bin" -s "$serial" shell am stopservice -n link.quantway.tincspike/.GuardVpnService >/dev/null 2>&1 || true
}
trap cleanup 0 1 2 15
instrumentation_log="$spike_out/instrumentation.log"
if ! "$adb_bin" -s "$serial" shell am instrument -w -r \
    -e test.run "${CLIENT_TINC_ANDROID_TEST_RUN:-.*}" \
    -e guard.vpn "${CLIENT_TINC_ANDROID_VPN_GUARD:-0}" \
    link.quantway.tincspike/.NativeTests >"$instrumentation_log" 2>&1; then
    cat "$instrumentation_log"
    exit 1
fi
cat "$instrumentation_log"
# am instrument itself may exit zero when the instrumentation or native tests
# fail. Require both explicit success markers and refuse failure diagnostics.
awk '
    /^INSTRUMENTATION_RESULT: native_exit_code=0\r?$/ { native_ok = 1 }
    /^INSTRUMENTATION_CODE: -1\r?$/ { instrumentation_ok = 1 }
    /INSTRUMENTATION_FAILED|INSTRUMENTATION_ABORTED|INSTRUMENTATION_RESULT: (shortMsg|longMsg)=/ { failed = 1 }
    END { exit !(native_ok && instrumentation_ok && !failed) }
' "$instrumentation_log"
