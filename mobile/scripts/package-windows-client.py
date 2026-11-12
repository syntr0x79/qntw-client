#!/usr/bin/env python3
"""Assemble one offline Windows install tree from frozen native/Go/runtime input."""
import argparse, hashlib, importlib.util, json, os, pathlib, re, shutil, subprocess, zipfile

def sha(p): return hashlib.sha256(p.read_bytes()).hexdigest()
def run(args, **kwargs): return subprocess.check_output(args, **kwargs)
def pe_machine(p):
    data=p.read_bytes()
    if data[:2] != b'MZ': raise RuntimeError('not Windows PE: '+str(p))
    offset=int.from_bytes(data[60:64],'little')
    if data[offset:offset+4] != b'PE\0\0': raise RuntimeError('invalid PE signature')
    return int.from_bytes(data[offset+4:offset+6],'little')

def collect_go_notices(repo, destination, arch):
    env=dict(os.environ,GOOS='windows',GOARCH=arch,CGO_ENABLED='0',GOWORK='off')
    data=run(['go','list','-mod=readonly','-deps','-json','./windows/cmd/quantway-launcher','./windows/cmd/quantway-vpn-helper','./linux/cmd/quantway'],cwd=repo/'mobile',env=env).decode()
    decoder=json.JSONDecoder();position=0;modules={}
    while position<len(data):
        while position<len(data) and data[position].isspace():position+=1
        if position==len(data):break
        package,position=decoder.raw_decode(data,position);m=package.get('Module')
        if not m or m.get('Main'):continue
        actual=m.get('Replace',m)
        directory=pathlib.Path(actual.get('Dir',m.get('Dir',''))).resolve()
        if directory in (repo,repo/'mobile'):continue
        modules[m['Path']]={'module':m['Path'],'version':m.get('Version'),'source':directory,'replacement':m.get('Replace',{}).get('Path')}
    index=[]
    for name,m in sorted(modules.items()):
        folder=destination/re.sub(r'[^A-Za-z0-9._-]','_',name)
        folder.mkdir(parents=True,exist_ok=True);files=[]
        for p in m.pop('source').iterdir():
            if p.is_file() and not p.is_symlink() and re.match(r'^(LICENSE|COPYING|NOTICE)([._-].*)?$',p.name,re.I):
                shutil.copyfile(p,folder/p.name);files.append({'name':p.name,'sha256':sha(p)})
        index.append(dict(m,license_files=files))
    goroot=pathlib.Path(run(['go','env','GOROOT'],text=True).strip())
    shutil.copyfile(goroot/'LICENSE',destination/'Go-LICENSE')
    (destination/'INDEX.json').write_text(json.dumps({'architecture':arch,'modules':index},indent=2)+'\n')

def main():
    p=argparse.ArgumentParser();p.add_argument('--repo',type=pathlib.Path,required=True);p.add_argument('--work',type=pathlib.Path,required=True);p.add_argument('--arch',choices=['amd64','arm64'],required=True);p.add_argument('--output',type=pathlib.Path,required=True);p.add_argument('--version',default='0.7.1-preview20261101.4')
    a=p.parse_args();repo=a.repo.resolve();root=a.output.resolve();root.mkdir(parents=True,exist_ok=True)
    runtime=a.work/('runtime-'+a.arch)
    shutil.copytree(runtime,root/'runtime',dirs_exist_ok=True)
    for name in ['quantway-helper.exe','quantway-net-helper.exe','quantway-tinc-helper.exe']:
        target=root/'bin'/name;target.parent.mkdir(exist_ok=True);shutil.copyfile(a.work/'go'/a.arch/name,target)
    shutil.copyfile(a.work/'go'/a.arch/'quantway.exe',root/'quantway.exe')
    native=repo/'bin'/'quantway-tinc-windows'/a.arch
    for name in ['tinc.exe','tincd.exe']:shutil.copyfile(native/name,root/'bin'/name)
    with zipfile.ZipFile(a.work/'wintun-0.14.1.zip') as z:
        assert hashlib.sha256((a.work/'wintun-0.14.1.zip').read_bytes()).hexdigest()=='07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51'
        (root/'bin'/'wintun.dll').write_bytes(z.read('wintun/bin/'+a.arch+'/wintun.dll'))
        (root/'licenses').mkdir(exist_ok=True)
        (root/'licenses'/'Wintun-LICENSE.txt').write_bytes(z.read('wintun/LICENSE.txt'))
    ui=root/'ui';ui.mkdir(exist_ok=True)
    for source in sorted((repo/'mobile/linux/ui').iterdir()):
        if source.is_file() and source.suffix in ['.py','.css'] and not source.name.startswith(('test_','qa_')):
            shutil.copyfile(source,ui/source.name)
    for name in ['bootstrap.py','pixbuf_cache.py','pixbuf-loaders.json']:
        shutil.copyfile(repo/'mobile/windows'/name,root/name)
    assets=root/'assets';assets.mkdir(exist_ok=True)
    shutil.copyfile(repo/'mobile/android/app/src/main/res/drawable/quantway_brand.png',assets/'quantway_brand.png')
    shutil.copyfile(repo/'mobile/windows/quantway.ico',assets/'quantway.ico')
    shutil.copyfile(repo/'mobile/native/tinc/COPYING',root/'licenses'/'tinc-COPYING')
    shutil.copyfile(repo/'bin/quantway-tinc-windows/tinc-windows-corresponding-source.tar.gz',root/'licenses'/'tinc-windows-corresponding-source.tar.gz')
    wfp_license=repo/'mobile/windows/network/wfp/LICENSE.WireGuard'
    if wfp_license.is_file():shutil.copyfile(wfp_license,root/'licenses'/'WireGuard-WFP-LICENSE')
    collect_go_notices(repo,root/'licenses'/'go',a.arch)
    shutil.copyfile(repo/'mobile/windows/runtime-lock.json',root/'licenses'/'msys2-runtime-lock.json')
    sys_path=str(repo/'mobile/windows')
    import sys
    sys.path.insert(0,sys_path)
    import pixbuf_cache
    pixbuf_cache.from_manifest(root,json.loads((root/'pixbuf-loaders.json').read_text()),a.arch,verify_hashes=True)
    shutil.copyfile(repo/'mobile/windows/README.txt',root/'README.txt')
    expected={'amd64':0x8664,'arm64':0xaa64}[a.arch]
    for path in [root/'quantway.exe',*(root/'bin').glob('*.exe'),root/'bin'/'wintun.dll',root/'runtime/bin/pythonw.exe',root/'runtime/bin/libgtk-4-1.dll']:
        if pe_machine(path)!=expected:raise RuntimeError('wrong PE architecture: '+str(path))
    info={'version':a.version,'architecture':'windows/'+a.arch,'signing':'Application and installer are not Authenticode signed; wintun.dll comes from the official signed distribution.','native':json.loads((native/'BUILD-INFO.json').read_text()),'files_sha256':{str(path.relative_to(root)).replace('\\','/'):sha(path) for path in sorted(root.rglob('*')) if path.is_file()}}
    (root/'BUILD-INFO.json').write_text(json.dumps(info,indent=2)+'\n')
    print(json.dumps({'architecture':a.arch,'files':len(info['files_sha256']),'tree_bytes':sum(p.stat().st_size for p in root.rglob('*') if p.is_file())}))
if __name__=='__main__':main()
