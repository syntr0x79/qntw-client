#!/bin/sh
set -eu
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
if [ ! -x "$here/build/quantway-helper" ]; then
  echo "Сначала соберите помощник: cd mobile && go build -o linux/build/quantway-helper ./linux/cmd/quantway" >&2
  exit 1
fi
exec /usr/bin/python3 "$here/ui/quantway.py"
