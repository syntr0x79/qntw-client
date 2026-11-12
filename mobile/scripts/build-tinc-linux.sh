#!/bin/sh
# Linux desktop packaging remains on the previous source until its own migration.
set -eu
repo=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
case "${TINC_CLIENT_SOURCE:-legacy}" in
  legacy) exec "$repo/mobile/scripts/build-tinc-linux-legacy.sh" "$@" ;;
  fork) exec "$repo/mobile/scripts/build-tinc-linux-fork.sh" "$@" ;;
  *) echo 'TINC_CLIENT_SOURCE must be legacy or fork' >&2; exit 2 ;;
esac
