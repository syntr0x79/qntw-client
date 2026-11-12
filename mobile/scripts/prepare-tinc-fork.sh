#!/bin/sh
# Export only the pinned committed source, never a caller's working tree.
set -eu
repo=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
. "$repo/mobile/native/tinc/source-pin.env"
destination=${1:?usage: prepare-tinc-fork.sh destination}
mkdir -p "$destination"
destination=$(CDPATH= cd -- "$destination" && pwd)
if [ -e "$destination/source" ]; then
  echo 'Refusing to overwrite a prepared source directory' >&2; exit 2
fi
upstream=${TINC_SOURCE_REPOSITORY:-${TINC_FORK_SOURCE_CACHE:-/private/tmp/quantway-tinc-fork-source}}
if [ ! -d "$upstream" ]; then
  mkdir -p "$upstream"
  git -C "$upstream" init -q
  git -C "$upstream" remote add origin "$TINC_FORK_URL"
fi
if ! git -C "$upstream" cat-file -e "$TINC_FORK_COMMIT^{commit}" 2>/dev/null; then
  if [ -n "${TINC_SOURCE_REPOSITORY:-}" ]; then
    echo 'Supplied source repository does not contain pinned fork commit' >&2; exit 2
  fi
  git -C "$upstream" fetch --depth 1 origin "$TINC_FORK_COMMIT"
fi
test "$(git -C "$upstream" rev-parse "$TINC_FORK_COMMIT^{commit}")" = "$TINC_FORK_COMMIT"
epoch=$(git -C "$upstream" show -s --format=%ct "$TINC_FORK_COMMIT")
git -C "$upstream" archive --format=tar "$TINC_FORK_COMMIT" > "$destination/source.tar"
mkdir "$destination/source"
tar -xf "$destination/source.tar" -C "$destination/source"
gzip -n -c "$destination/source.tar" > "$destination/tinc-source.tar.gz"
rm "$destination/source.tar"
{
  echo "source_url=$TINC_FORK_URL"
  echo "fork_commit=$TINC_FORK_COMMIT"
  echo "version=$TINC_FORK_VERSION"
  echo "source_date_epoch=$epoch"
  echo "source_work_directory=$destination/source"
} > "$destination/BUILD-INFO.txt"
printf '%s\n' "$destination/source"
