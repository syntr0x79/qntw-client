#!/bin/sh
# Verify the pinned fork and export its source/build receipt to the child.
set -eu
scripts=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
exec python3 "$scripts/prepare-xray-darwin.py" --exec "$@"
