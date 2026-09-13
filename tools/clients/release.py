#!/usr/bin/env python3
"""Build local release artifacts; does not publish packages or create tags."""
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import zipfile

ROOT=Path(__file__).resolve().parents[2]
config=json.loads((ROOT/'tools/clients/config.json').read_text())
version=config['version']
destination=ROOT/'dist/clients'/version
def run(*args,cwd=ROOT,env=None): subprocess.run(args,cwd=cwd,env=env,check=True)
run('python3','tools/clients/check.py')
destination.mkdir(parents=True,exist_ok=True)
with tempfile.TemporaryDirectory(prefix='wave-release-') as directory:
    stage=Path(directory)
    for goos in ('linux','darwin','windows'):
        for goarch in ('amd64','arm64'):
            name='wavectl'+('.exe' if goos=='windows' else '')
            binary=stage/name
            env=dict(os.environ,GOOS=goos,GOARCH=goarch,CGO_ENABLED='0')
            run('go','build','-trimpath','-ldflags',f'-s -w -X main.version={version}','-o',str(binary),'.',cwd=ROOT/'cli',env=env)
            archive=f'wavectl_{version}_{goos}_{goarch}'
            if goos=='windows':
                with zipfile.ZipFile(destination/(archive+'.zip'),'w',zipfile.ZIP_DEFLATED) as z: z.write(binary,name)
            else:
                with tarfile.open(destination/(archive+'.tar.gz'),'w:gz') as tar: tar.add(binary,arcname=name)
    for skill in ('wave-client','wave-admin'):
        with zipfile.ZipFile(destination/f'{skill}_{version}.zip','w',zipfile.ZIP_DEFLATED) as z:
            for file in sorted((ROOT/'skills'/skill).rglob('*')):
                if file.is_file(): z.write(file,file.relative_to(ROOT/'skills'/skill))
    run('uv','build','--out-dir',str(stage/'python'),cwd=ROOT/'sdks/python')
    for file in (stage/'python').iterdir(): shutil.copy(file,destination/file.name)
    run('npm','run','build',cwd=ROOT/'sdks/typescript')
    run('npm','pack','--ignore-scripts','--pack-destination',str(destination),cwd=ROOT/'sdks/typescript')
    with tarfile.open(destination/f'wave-go_{version}.tar.gz','w:gz') as tar:
        for file in sorted((ROOT/'sdks/go').rglob('*')):
            if file.is_file() and file.suffix in ('.go','.mod','.sum','.json','.md'): tar.add(file,arcname=Path('wave-go')/file.relative_to(ROOT/'sdks/go'))
    shutil.copy(ROOT/'api/openapi.json',destination/'openapi.json')
    shutil.copy(ROOT/'api/openapi.zh-CN.json',destination/'openapi.zh-CN.json')
    (destination/'openapi.en.json').unlink(missing_ok=True)
    manifest=json.loads((ROOT/'tools/clients/manifest.json').read_text())
    manifest.update(packages={key:config[key] for key in ('goModule','pythonPackage','npmPackage')},repository='https://github.com/ni00/wave-ai',serverContractVersion='1.0')
    (destination/'manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
    checksums=''.join(f'{hashlib.sha256(f.read_bytes()).hexdigest()}  {f.name}\n' for f in sorted(destination.iterdir()) if f.is_file() and f.name!='SHA256SUMS')
    (destination/'SHA256SUMS').write_text(checksums)
print(f'Local artifacts: {destination}')
