#!/bin/sh
# Complete offline Windows installers. Build has no access to production/VMs.
set -eu
repo=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
arch=${1:-all}
case "$arch" in all) architectures='amd64 arm64';; amd64|arm64) architectures=$arch;; *) echo 'usage: build-windows-client.sh amd64|arm64|all' >&2;exit 2;; esac
work=${QUANTWAY_WINDOWS_BUILD_DIR:-/private/tmp/quantway-windows-build-20261101}
output=${QUANTWAY_WINDOWS_OUTPUT_DIR:-"$repo/bin/quantway-windows-client"}
version=${QUANTWAY_WINDOWS_VERSION:-0.7.1-preview20261101.4}
image=${QUANTWAY_WINDOWS_BUILDER_IMAGE:-quantway-windows-builder:llvm20261020}
mkdir -p "$work/go" "$output"
python3 "$repo/mobile/windows/source_freeze.py" write --repo "$repo" --output "$work/SOURCE-FREEZE.json"
"$repo/mobile/scripts/build-tinc-windows.sh" "$arch"
for architecture in $architectures; do
 python3 "$repo/mobile/windows/runtime.py" stage --cache "$work/packages" --lock "$repo/mobile/windows/runtime-lock.json" --arch "$architecture" --output "$work/runtime-$architecture"
 # Binary schema caches are platform-independent and generated using the recorded builder tools.
 docker run --rm --network none -v "$work/runtime-$architecture:/runtime" "$image" sh -eu -c 'glib-compile-schemas --strict /runtime/share/glib-2.0/schemas; XDG_DATA_DIRS=/runtime/share update-mime-database /runtime/share/mime'
 mkdir -p "$work/go/$architecture"
 (
  cd "$repo/mobile"
  export GOOS=windows GOARCH="$architecture" CGO_ENABLED=0
  export GOCACHE="${GOCACHE:-/private/tmp/quantway-windows-go-cache}"
  launcher="$work/launcher-$architecture"
  mkdir -p "$launcher"
  cp "$repo/mobile/windows/cmd/quantway-launcher/main_windows.go" "$launcher/"
  cp "$repo/mobile/windows/quantway.ico" "$repo/mobile/windows/quantway.manifest" "$repo/mobile/windows/launcher.rc" "$launcher/"
  printf 'module link.quantway.launcher\n\ngo 1.27\n' > "$launcher/go.mod"
  case "$architecture" in amd64) target=x86_64-w64-mingw32;; arm64) target=aarch64-w64-mingw32;;esac
  docker run --rm --network none -v "$launcher:/work" "$image" "$target-windres" launcher.rc -O coff -o "resource_windows_$architecture.syso"
  (cd "$launcher" && go build -trimpath -ldflags '-s -w -H windowsgui' -o "$work/go/$architecture/quantway.exe" .)
  go build -trimpath -ldflags '-s -w -H windowsgui' -o "$work/go/$architecture/quantway-helper.exe" ./linux/cmd/quantway
  for mode in net tinc; do
   go build -trimpath -ldflags '-s -w -H windowsgui' -o "$work/go/$architecture/quantway-$mode-helper.exe" ./windows/cmd/quantway-vpn-helper
  done
 )
 bundle="$work/package-$architecture"
 if [ -d "$bundle" ]; then echo "existing package stage: choose a fresh QUANTWAY_WINDOWS_BUILD_DIR" >&2;exit 1;fi
 python3 "$repo/mobile/scripts/package-windows-client.py" --repo "$repo" --work "$work" --arch "$architecture" --output "$bundle" --version "$version"
 docker run --rm --network none -v "$bundle:/bundle:ro" -v "$output:/out" -v "$repo/mobile/windows:/src:ro" "$image" makensis "-DVERSION=$version" "-DARCH=$architecture" -DBUNDLE=/bundle "-DOUTPUT=/out/Quantway-$version-$architecture-Setup.exe" /src/installer.nsi
 cp "$bundle/BUILD-INFO.json" "$output/BUILD-INFO-$architecture.json"
done
python3 "$repo/mobile/windows/source_freeze.py" verify --repo "$repo" --output "$work/SOURCE-FREEZE.json"
python3 - "$repo" "$work/SOURCE-FREEZE.json" "$output" "$version" <<'PY'
import hashlib,json,pathlib,sys
repo,freeze,out=map(pathlib.Path,sys.argv[1:4]);h=lambda p:hashlib.sha256(p.read_bytes()).hexdigest()
for name,expected in json.loads(freeze.read_text()).items():
 if h(repo/name)!=expected:raise SystemExit('source changed during build: '+name)
artifacts={p.name:{'sha256':h(p),'size_bytes':p.stat().st_size} for p in sorted(out.glob('Quantway-'+sys.argv[4]+'-*-Setup.exe'))}
(out/'SHA256SUMS').write_text(''.join(v['sha256']+'  '+n+'\n' for n,v in artifacts.items()))
(out/'BUILD-RESULT.json').write_text(json.dumps({'version':sys.argv[4],'state':'built_not_yet_native_windows_validated','artifacts':artifacts,'source_freeze_sha256':h(freeze),'sources':json.loads(freeze.read_text()),'production_changes':False},indent=2)+'\n')
print(json.dumps({'installers':artifacts}))
PY
