#!/usr/bin/env python3
"""Run a GTK assertion fixture without claiming the desktop D-Bus application ID.

Fixtures can create several application instances in one test process. Production
keeps its normal single-instance application ID; only this test runner removes it.
"""
from pathlib import Path
import runpy
import sys

from quantway import Quantway

original_init = Quantway.__init__


def isolated_init(self, *args, **kwargs):
    original_init(self, *args, **kwargs)
    self.set_application_id(None)


if __name__ == '__main__':
    if len(sys.argv) != 2:
        raise SystemExit('usage: run_fixture.py test_name.py')
    path = Path(sys.argv[1])
    if path.parent != Path('.') or not path.name.startswith('test_') or path.suffix != '.py':
        raise SystemExit('expected a local test_*.py fixture')
    Quantway.__init__ = isolated_init
    sys.argv = [str(path)]
    runpy.run_path(str(path), run_name='__main__')
