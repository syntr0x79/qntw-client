"""Hash linked repository Go sources and packaged desktop/build resources."""
import argparse, hashlib, json, os
from pathlib import Path
import subprocess

def snapshot(repo):
    paths=set()
    for group in ['mobile/windows','mobile/linux/ui','mobile/scripts']:
        for path in (repo/group).rglob('*'):
            if path.is_file() and '__pycache__' not in path.parts and path.suffix!='.pyc':
                paths.add(path)
    for group in ['deploy/tinc/patches','deploy/tinc-client/patches','mobile/native/tinc/patches']:
        paths.update((repo/group).glob('*.patch'))
    paths.update(repo/n for n in ['mobile/native/tinc/source-pin.env','mobile/native/tinc/COPYING','go.mod','go.sum','mobile/go.mod','mobile/go.sum','mobile/android/app/src/main/res/drawable/quantway_brand.png'])
    for arch in ['amd64','arm64']:
        env=dict(os.environ,GOOS='windows',GOARCH=arch,CGO_ENABLED='0',GOWORK='off')
        text=subprocess.check_output(['go','list','-mod=readonly','-deps','-json','./windows/cmd/quantway-launcher','./windows/cmd/quantway-vpn-helper','./linux/cmd/quantway'],cwd=repo/'mobile',env=env,text=True)
        decoder=json.JSONDecoder();position=0
        while position<len(text):
            while position<len(text) and text[position].isspace():position+=1
            if position==len(text):break
            package,position=decoder.raw_decode(text,position)
            directory=Path(package.get('Dir','/')).resolve()
            if not directory.is_relative_to(repo):continue
            for field in ['GoFiles','CgoFiles','CFiles','HFiles','SFiles','SysoFiles','EmbedFiles']:
                paths.update(directory/name for name in package.get(field,[]))
    return {str(p.relative_to(repo)).replace('\\','/'):hashlib.sha256(p.read_bytes()).hexdigest() for p in sorted(paths)}

if __name__=='__main__':
    parser=argparse.ArgumentParser();parser.add_argument('action',choices=['write','verify']);parser.add_argument('--repo',required=True,type=Path);parser.add_argument('--output',required=True,type=Path);a=parser.parse_args()
    current=snapshot(a.repo.resolve())
    if a.action=='write':a.output.write_text(json.dumps(current,indent=2)+'\n')
    else:
        previous=json.loads(a.output.read_text())
        different=sorted(k for k in current.keys()|previous.keys() if current.get(k)!=previous.get(k))
        if different:raise SystemExit('Sources changed during Windows build: '+', '.join(different))
    print('Windows source freeze: '+str(len(current))+' files, '+a.action)
