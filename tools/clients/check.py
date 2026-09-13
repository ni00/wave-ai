#!/usr/bin/env python3
"""Check release metadata and the invariants that code generators cannot infer."""
import json
from pathlib import Path
import re

ROOT=Path(__file__).resolve().parents[2]
def read(path): return (ROOT/path).read_text()
config=json.loads(read('tools/clients/config.json'))
spec=json.loads(read('api/openapi.json'))
chinese=json.loads(read('api/openapi.zh-CN.json'))

def without_documentation(value):
    if isinstance(value,list): return [without_documentation(item) for item in value]
    if not isinstance(value,dict): return value
    return {key: child if key in ('example','examples','default','enum','const') else without_documentation(child)
            for key,child in value.items()
            if not (key in ('description','summary') and isinstance(child,str))}

assert without_documentation(spec)==without_documentation(chinese),'language versions changed the wire contract'
for path,methods in spec['paths'].items():
    for method,op in methods.items():
        assert op.get('summary') and not re.search(r'[\u4e00-\u9fff]| \|\| ',op['summary']),f'untranslated summary: {method} {path}'

for directory in ('.','cli','tools/clients','sdks/go','sdks/python','sdks/typescript'):
    for filename,peer in (('README.md','README.zh-CN.md'),('README.zh-CN.md','README.md')):
        file=ROOT/directory/filename
        text=file.read_text()
        assert f']({peer})' in text,f'missing language switch: {file}'
        for relative in re.findall(r'\]\(([^)]+)\)',text):
            if '://' in relative or relative.startswith('#'): continue
            assert (file.parent/relative.split('#')[0]).exists(),f'broken README link: {file}: {relative}'
assert spec['components']['securitySchemes']['BearerAuth']=={'type':'http','scheme':'bearer'}
assert f"module {config['goModule']}\n" in read('sdks/go/go.mod')
assert f"version = \"{config['version']}\"" in read('sdks/python/pyproject.toml')
assert f"name = \"{config['pythonPackage']}\"" in read('sdks/python/pyproject.toml')
npm=json.loads(read('sdks/typescript/package.json'))
assert npm['version']==config['version'] and npm['name']==config['npmPackage']
assert re.search(r'var version\s*=\s*"'+re.escape(config['version'])+'"',read('cli/main.go'))
for path,methods in spec['paths'].items():
    for method,op in methods.items():
        for parameter in op.get('parameters',[]):
            if parameter['name']=='after': assert 'default' not in parameter['schema']
        if op['operationId'] in ('filesContent','skillsContent'):
            assert set(op['responses']['200']['content'])=={'application/octet-stream'}
            assert set(op['responses']['401']['content'])=={'application/json'}
        if op['operationId']=='executionStreamEvents':
            assert set(op['responses']['200']['content'])=={'text/event-stream'}
for name in ('wave-client','wave-admin'):
    text=read(f'skills/{name}/SKILL.md')
    assert text.startswith(f'---\nname: {name}\ndescription: ')
    assert 'TODO' not in text and len(text.splitlines())<150
    skill=ROOT/'skills'/name
    for file in skill.rglob('*'):
        if file.is_file() and file.suffix in ('.md','.yaml'):
            assert not re.search(r'[\u3400-\u9fff]',file.read_text()),f'skill instructions must be English: {file}'
    for markdown in skill.rglob('*.md'):
        for relative in re.findall(r'\]\(([^)]+)\)',markdown.read_text()):
            if '://' in relative or relative.startswith('#'): continue
            target=(markdown.parent/relative.split('#')[0]).resolve()
            assert target.is_relative_to(ROOT) and target.exists(),f'broken skill link: {markdown}: {relative}'
    ui=(skill/'agents/openai.yaml').read_text()
    assert all(field in ui for field in ('display_name:', 'short_description:', 'default_prompt:'))
    assert '$'+name in ui
json.loads(read('skills/wave-client/assets/client-tool.json'))
print('Client metadata, contract and skill checks passed')
