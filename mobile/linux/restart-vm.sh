#!/bin/sh
set -eu
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
pkill -f '[/]usr/bin/python3 .*/linux/ui/quantway.py' || true
exec /usr/bin/python3 "$here/ui/quantway.py"
