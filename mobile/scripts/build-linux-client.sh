#!/bin/sh
# Integrated GTK client. No account data, credentials, keys or runtime state enter the bundle.
set -eu
repo=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
architecture=${1:-arm64}
case "$architecture" in
  arm64|amd64) architectures=$architecture ;;
  all) architectures='arm64 amd64' ;;
  *) echo 'usage: build-linux-client.sh arm64|amd64|all' >&2; exit 2 ;;
esac
. "$repo/mobile/native/tinc/source-pin.env"
output=${QUANTWAY_LINUX_OUTPUT_DIR:-"$repo/bin/quantway-linux-client"}
work=${QUANTWAY_LINUX_BUILD_DIR:-/private/tmp/quantway-linux-client}
mkdir -p "$output" "$work"
stage=$(mktemp -d "$work/package.XXXXXX")
trap 'rm -rf "$stage"' EXIT HUP INT TERM
for arch in $architectures; do
  native="$repo/bin/quantway-tinc-linux/linux-$arch"
  if [ ! -f "$native/tinc" ] || [ ! -f "$native/tincd" ] || [ ! -f "$native/SHA256SUMS" ]; then
    TINC_LINUX_OUTPUT_DIR="$repo/bin/quantway-tinc-linux" "$repo/mobile/scripts/build-tinc-linux.sh" "$arch"
  fi
  # Reusing a native bundle requires its full checksum manifest and the exact source pin.
  (cd "$native" && shasum -a 256 -c SHA256SUMS)
  python3 - "$native/BUILD-INFO.txt" "$TINC_CLIENT_VERSION" "$TINC_UPSTREAM_COMMIT" "$arch" <<'PY'
from pathlib import Path
import sys
lines = Path(sys.argv[1]).read_text().splitlines()
for expected in ("client_tinc_version=" + sys.argv[2], "tinc_upstream_commit=" + sys.argv[3],
                 "architecture=linux/" + sys.argv[4], "tcp_relay_patch=1"):
    if lines.count(expected) != 1:
        raise SystemExit("native bundle does not match pinned guarded tinc: " + expected)
PY
  root="$stage/quantway-linux-$arch"
  linux="$root/mobile/linux"
  mkdir -p "$linux/build" "$linux/ui" "$root/mobile/android/app/src/main/res/drawable"
  (cd "$repo/mobile" && GOOS=linux GOARCH="$arch" CGO_ENABLED=0 GOCACHE="${GOCACHE:-/private/tmp/quantway-linux-client-go-cache}" go list -deps ./linux/cmd/quantway-tinc-helper) > "$stage/tinc-helper-deps-$arch.txt"
  python3 - "$stage/tinc-helper-deps-$arch.txt" <<'PY'
from pathlib import Path
import sys
if "github.com/syntr0x79/qntw-client/mobile/engine" in Path(sys.argv[1]).read_text().splitlines():
    raise SystemExit("dedicated tinc helper must not import mobile/engine: its global Xray dialer breaks the isolated transport")
PY
  python3 - "$repo" "$linux/build/BUILD-INFO.client" "$arch" <<'PY'
import hashlib
from pathlib import Path
import sys
repo, output = map(Path, sys.argv[1:3])
paths = []
for directory in ("mobile/linux", "mobile/clienttinc", "internal/clienttincwire"):
    paths.extend(p for p in (repo / directory).rglob("*.go") if not p.name.endswith("_test.go"))
paths.extend(repo / name for name in ("go.mod", "go.sum", "mobile/go.mod", "mobile/go.sum"))
lines = ["architecture=linux/" + sys.argv[3]]
lines.extend(hashlib.sha256(p.read_bytes()).hexdigest() + "  " + p.relative_to(repo).as_posix() for p in sorted(paths))
output.write_text("\n".join(lines) + "\n")
PY
  for command in quantway quantway-net-helper quantway-tinc-helper; do
    binary=$command
    if [ "$command" = quantway ]; then binary=quantway-helper; fi
    (cd "$repo/mobile" && GOOS=linux GOARCH="$arch" CGO_ENABLED=0 GOCACHE="${GOCACHE:-/private/tmp/quantway-linux-client-go-cache}" go build -trimpath -ldflags '-s -w' -o "$linux/build/$binary" "./linux/cmd/$command")
  done
  python3 - "$repo" "$linux/build/BUILD-INFO.client" <<'PY'
import hashlib
from pathlib import Path
import sys
repo, info = map(Path, sys.argv[1:])
for line in info.read_text().splitlines()[1:]:
    checksum, relative = line.split("  ", 1)
    if hashlib.sha256((repo / relative).read_bytes()).hexdigest() != checksum:
        raise SystemExit("client source changed during build; rerun after source freeze: " + relative)
PY
  cp "$native/tinc" "$native/tincd" "$linux/build/"
  cp "$native/COPYING" "$linux/build/COPYING.tinc"
  # Keep provenance without absolute build-machine paths from the native build log.
  sed -n '/^client_tinc_version=/p; /^tinc_upstream_commit=/p; /^tcp_relay_patch=/p; /^architecture=/p' "$native/BUILD-INFO.txt" > "$linux/build/BUILD-INFO.tinc"
  printf '%s\n' "$arch" > "$linux/build/ARCH"
  cp "$repo/mobile/linux/run.sh" "$repo/mobile/linux/setup-vm.sh" "$repo/mobile/linux/README.md" "$linux/"
  python3 - "$repo/mobile/linux/ui" "$linux/ui" <<'PY'
from pathlib import Path
import shutil
import sys
source, target = map(Path, sys.argv[1:])
for path in sorted(source.rglob("*")):
    if path.is_symlink():
        raise SystemExit("UI symlinks are not packaged: " + str(path))
    if path.is_file() and path.suffix in (".py", ".css") and "__pycache__" not in path.parts:
        destination = target / path.relative_to(source)
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(path, destination)
PY
  cp "$repo/mobile/android/app/src/main/res/drawable/quantway_brand.png" "$root/mobile/android/app/src/main/res/drawable/quantway_brand.png"
  chmod 0755 "$linux/run.sh" "$linux/setup-vm.sh" "$linux/build/quantway-helper" "$linux/build/quantway-net-helper" "$linux/build/quantway-tinc-helper" "$linux/build/tinc" "$linux/build/tincd"
  python3 - "$root" "$linux" <<'PY'
import hashlib
import os
from pathlib import Path
import sys
root, linux = map(Path, sys.argv[1:])
lines = []
for path in sorted(root.rglob("*")):
    if path.is_file():
        lines.append(hashlib.sha256(path.read_bytes()).hexdigest() + "  " + os.path.relpath(path, linux))
(linux / "SHA256SUMS").write_text("\n".join(lines) + "\n")
PY
  /bin/sh "$linux/setup-vm.sh" --check
  COPYFILE_DISABLE=1 tar --no-xattrs -czf "$output/.quantway-linux-$arch.tar.gz.tmp" -C "$stage" "quantway-linux-$arch"
  mv "$output/.quantway-linux-$arch.tar.gz.tmp" "$output/quantway-linux-$arch.tar.gz"
  echo "Linux GTK client: $output/quantway-linux-$arch.tar.gz"
done
(cd "$output" && shasum -a 256 quantway-linux-*.tar.gz > SHA256SUMS.tmp && mv SHA256SUMS.tmp SHA256SUMS)
