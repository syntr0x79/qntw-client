"""Compile the actual script.c in isolation; inspect unresolved process APIs."""
import argparse
from pathlib import Path
import re
import subprocess

p=argparse.ArgumentParser();p.add_argument('--source',required=True,type=Path);p.add_argument('--compiler-prefix',required=True);a=p.parse_args()
source=a.source.resolve();output=source/'script-nohooks-check.o'
subprocess.run([a.compiler_prefix+'-clang','-DHAVE_CONFIG_H','-DQUANTWAY_WINDOWS_CLIENT=1','-I'+str(source),'-O2','-c',str(source/'src/script.c'),'-o',str(output)],check=True)
symbols=subprocess.check_output([a.compiler_prefix+'-nm','--undefined-only',str(output)],text=True)
# Other script.c functions format in-memory routing metadata; none should
# inspect caller environment, test hook files, or launch an interpreter.
for bad in ['system','getenv','access','putenv','exec','spawn','CreateProcess','ShellExecute']:
 if re.search(r'(?i)(?:^|[_\s])'+re.escape(bad)+r'(?:[AW]|_s)?(?:$|\s)',symbols,re.M):
  raise SystemExit('Forbidden process/hook dependency: '+bad+'\n'+symbols)
print('PASS: actual Windows script.c object has no environment/hook/process dependencies')
preprocessed=subprocess.check_output([a.compiler_prefix+'-clang','-DHAVE_CONFIG_H','-DQUANTWAY_WINDOWS_CLIENT=1','-I'+str(source),'-E',str(source/'src/tincctl.c')],text=True)
start=preprocessed.index('static int cmd_stop(')
body_start=preprocessed.index('{',start); depth=1; end=body_start+1
while depth:
 depth+=(preprocessed[end]=='{')-(preprocessed[end]=='}');end+=1
body=preprocessed[body_start:end]
if 'return stop_tincd() ? 0 : 1;' not in body or 'remove_service' in body:
 raise SystemExit('Client stop must only use foreground control protocol: '+body)
print('PASS: preprocessed Windows client cmd_stop bypasses all SCM removal')
