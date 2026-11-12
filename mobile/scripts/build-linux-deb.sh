#!/bin/sh
# Build distributable desktop packages; never installs or starts a VPN.
set -eu
repo=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
case "${1:-all}" in
  arm64|amd64) architectures=$1 ;;
  all) architectures='arm64 amd64' ;;
  *) echo 'usage: build-linux-deb.sh arm64|amd64|all' >&2; exit 2 ;;
esac
version=${QUANTWAY_DEB_VERSION:-0.7.1~preview20261101.2}
maintainer=${QUANTWAY_DEB_MAINTAINER:-Quantway}
output=${QUANTWAY_DEB_OUTPUT_DIR:-"$repo/bin/quantway-linux-client/deb"}
work=${QUANTWAY_DEB_BUILD_DIR:-"${TMPDIR:-/tmp}/quantway-linux-deb"}
builder=${QUANTWAY_DEB_BUILDER_IMAGE:-debian:bookworm-slim}
mkdir -p "$output" "$work"
output=$(CDPATH= cd -- "$output" && pwd)
work=$(CDPATH= cd -- "$work" && pwd)
stage=$(mktemp -d "$work/package.XXXXXX")
trap 'rm -rf "$stage"' EXIT HUP INT TERM
if [ -n "${QUANTWAY_DEB_BUNDLE_DIR:-}" ]; then
  bundles=$QUANTWAY_DEB_BUNDLE_DIR
else
  bundles="$stage/bundles"
  QUANTWAY_LINUX_OUTPUT_DIR="$bundles" QUANTWAY_LINUX_BUILD_DIR="$work/client-build" \
    sh "$repo/mobile/scripts/build-linux-client.sh" "${1:-all}"
fi
go_binary=${QUANTWAY_DEB_GO_BINARY:-}
if [ -z "$go_binary" ]; then
  go_root=$(cd "$repo/mobile" && go env GOROOT)
  go_binary="$go_root/bin/go"
fi
python3 "$repo/mobile/scripts/collect-linux-deb-sources.py" --output "$stage/sources" --go-binary "$go_binary"
go_root=$("$go_binary" env GOROOT)
cp "$go_root/LICENSE" "$stage/sources/third-party-licenses/Go-LICENSE"
if [ -f "$go_root/PATENTS" ]; then
  cp "$go_root/PATENTS" "$stage/sources/third-party-licenses/Go-PATENTS"
fi
export SOURCE_DATE_EPOCH=${SOURCE_DATE_EPOCH:-$(date -u +%s)}
for arch in $architectures; do
  python3 "$repo/mobile/scripts/package-linux-deb.py" --repo "$repo" \
    --archive "$bundles/quantway-linux-$arch.tar.gz" --sources "$stage/sources" \
    --stage "$stage/quantway-$arch" --info "$output/BUILD-INFO-$arch.json" \
    --arch "$arch" --version "$version" --maintainer "$maintainer"
  docker run --rm --network none --read-only --cap-drop ALL --security-opt no-new-privileges \
    --cpus 2 --memory 1g --pids-limit 64 --tmpfs /tmp:rw,size=512m \
    -e SOURCE_DATE_EPOCH -e DPKG_DEB_THREADS_MAX=2 \
    --mount "type=bind,src=$stage,dst=/stage,readonly" \
    --mount "type=bind,src=$output,dst=/out" \
    --entrypoint /usr/bin/dpkg-deb "$builder" -Zxz -z3 --root-owner-group \
    --build "/stage/quantway-$arch" "/out/quantway_${version}_${arch}.deb"
done
python3 - "$output" <<'PY'
import hashlib
from pathlib import Path
import sys
directory = Path(sys.argv[1])
text = ''.join(hashlib.sha256(p.read_bytes()).hexdigest() + '  ' + p.name + '\n'
               for p in sorted(directory.glob('quantway_*.deb')))
temporary = directory / 'SHA256SUMS.tmp'
temporary.write_text(text)
temporary.replace(directory / 'SHA256SUMS')
PY
echo "DEB packages: $output"
