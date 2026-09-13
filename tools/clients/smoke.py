#!/usr/bin/env python3
"""Install the built Python/npm packages and execute the native CLI archive."""
import hashlib
import json
from pathlib import Path
import platform
import subprocess
import tarfile
import tempfile
import zipfile

ROOT=Path(__file__).resolve().parents[2]
version=json.loads((ROOT/'tools/clients/config.json').read_text())['version']
artifacts=ROOT/'dist/clients'/version
def run(*args,cwd=ROOT): subprocess.run(args,check=True,cwd=cwd)
for line in (artifacts/'SHA256SUMS').read_text().splitlines():
    checksum,name=line.split('  ',1)
    assert hashlib.sha256((artifacts/name).read_bytes()).hexdigest()==checksum,name
for name in ('openapi.json','openapi.zh-CN.json'):
    assert (artifacts/name).read_bytes()==(ROOT/'api'/name).read_bytes(),f'stale contract archive: {name}'
with tempfile.TemporaryDirectory(prefix='wave-install-smoke-') as directory:
    work=Path(directory)
    run('uv','venv',str(work/'venv'))
    python=work/'venv/bin/python'
    run('uv','pip','install','--python',str(python),str(next(artifacts.glob('*.whl'))))
    run(str(python),'-c','from wave_ai import Client, AsyncClient, OPERATIONS; from wave_ai_generated import AgentsApi; assert len(OPERATIONS)==54; assert Client().raw().configuration.host == "http://localhost:8080"',cwd=work)
    (work/'package.json').write_text('{"private":true,"type":"module"}')
    package=json.loads((ROOT/'sdks/typescript/package.json').read_text())['name']
    run('npm','install','--ignore-scripts',str(next(artifacts.glob('*.tgz'))),cwd=work)
    run('node','--input-type=module','-e',f'import {{Client,operations,generated}} from {json.dumps(package)}; if(Object.keys(operations).length!==54 || !new generated.AgentsApi(new Client().rawConfiguration())) process.exit(1);',cwd=work)
    system={'Linux':'linux','Darwin':'darwin','Windows':'windows'}[platform.system()]
    arch={'x86_64':'amd64','AMD64':'amd64','aarch64':'arm64','arm64':'arm64'}[platform.machine()]
    filename=f'wavectl_{version}_{system}_{arch}'
    if system=='windows':
        with zipfile.ZipFile(artifacts/(filename+'.zip')) as z: z.extract('wavectl.exe',work)
        binary=work/'wavectl.exe'
    else:
        binary=work/'wavectl'
        with tarfile.open(artifacts/(filename+'.tar.gz')) as tar: binary.write_bytes(tar.extractfile('wavectl').read())
        binary.chmod(0o755)
    output=subprocess.check_output([str(binary),'schema'],text=True)
    assert len(json.loads(output)['operations'])==54
    for skill in ('wave-client','wave-admin'):
        with zipfile.ZipFile(artifacts/f'{skill}_{version}.zip') as z:
            assert z.read('SKILL.md').startswith(f'---\nname: {skill}\n'.encode())
print('Built CLI, Python wheel, npm package and Skill archives passed installation smoke checks')
