#!/usr/bin/env python3
"""Generate only owned files, in a temporary directory; --check never writes them."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[2]
CONFIG = json.loads((ROOT / 'tools/clients/config.json').read_text())

def run(*args):
    subprocess.run(args, cwd=ROOT, check=True)

def dump(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + '\n')

def command_reference(spec, operations):
    lines = ['# API command reference (generated)', '',
             'Find API commands by resource. Use `wavectl <resource> <command> --help` for installed flags,',
             '`wavectl schema <resource> <command> --request` for request fields, or `--example` for a template.',
             'Use `--dry-run` to validate and preview a request. For workflow commands, follow the task and tool references linked from SKILL.md.', '']
    groups = {}
    for name, op in operations.items():
        groups.setdefault(op['command'].split()[0], []).append((name, op))
    for group, entries in sorted(groups.items()):
        lines.extend([f'## {group}', '', '| Command | Required path flags | Required body fields | Description |', '| --- | --- | --- | --- |'])
        for name, op in entries:
            paths = ', '.join('`--' + p['name'].replace('_', '-').lower() + '`' for p in op['parameters'] if p['in'] == 'path') or '—'
            request = (op.get('requestBody') or {}).get('content', {}).get('application/json', {}).get('schema', {})
            if '$ref' in request:
                request = spec['components']['schemas'][request['$ref'].split('/')[-1]]
            body = ', '.join('`--' + key.replace('_', '-') + '`' for key in request.get('required', [])) or '—'
            if op.get('transport') == 'upload': body = '`--file`'
            if op.get('transport') == 'download': body = '`--output`'
            lines.append(f"| `wavectl {op['command']}` | {paths} | {body} | {op['summary'].replace('|', '/')} |")
        lines.append('')
    lines += ['Path IDs also accept positional arguments in the order shown by help; session/task IDs support `--session` / `--task` aliases.',
              'Choose field flags or full `--body @file.json`; do not combine them.',
              'Paginated commands support `--all --format jsonl`; uploads support `--file - --filename NAME`.', '']
    return '\n'.join(lines)

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--check', action='store_true')
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix='wave-generate-') as directory:
        stage = Path(directory)
        stage.chmod(0o755)
        specfile = stage / 'api/openapi.json'
        specfile.parent.mkdir()
        run('node', 'tools/clients/convert.cjs', 'internal/platform/apidocs/swagger.json', str(specfile))
        chinese = stage / 'api/openapi.zh-CN.json'
        run('node', 'tools/clients/convert.cjs', 'internal/platform/apidocs/swagger.json', str(chinese), 'zh-CN')
        spec = json.loads(specfile.read_text())
        semantics = json.loads((ROOT / 'tools/clients/operations.json').read_text())
        operations = {}
        for path, methods in spec['paths'].items():
            for method, op in methods.items():
                name = op['operationId']
                if name in operations:
                    raise ValueError(f'duplicate operationId: {name}')
                operations[name] = dict(semantics[name], id=name, method=method.upper(), path=path,
                    summary=op.get('summary', ''), parameters=op.get('parameters', []),
                    requestBody=op.get('requestBody'), responses=op['responses'],
                    authenticated=bool(op.get('security', spec.get('security', []))))
        if operations.keys() != semantics.keys():
            raise ValueError('stale operation mapping: ' + str(semantics.keys() - operations.keys()))
        commands = [v['command'] for v in operations.values()]
        if len(commands) != len(set(commands)):
            raise ValueError('duplicate CLI command')
        docker = ['docker', 'run', '--rm', '--network', 'none', '--user', f'{os.getuid()}:{os.getgid()}',
                  '-v', f'{stage}:/local', CONFIG['generatorImage']]
        run(*docker, 'validate', '-i', '/local/api/openapi.json')
        run(*docker, 'validate', '-i', '/local/api/openapi.zh-CN.json')
        targets = {
            'go': {'packageName': 'generated', 'isGoSubmodule': True},
            'python': {'packageName': 'wave_ai_generated', 'packageVersion': CONFIG['version']},
            'typescript-fetch': {'npmName': CONFIG['npmPackage'], 'npmVersion': CONFIG['version']},
        }
        for target, settings in targets.items():
            settings.update(hideGenerationTimestamp=True, disallowAdditionalPropertiesIfNotPresent=False)
            configfile = stage / f'{target}.json'
            dump(configfile, settings)
            run(*docker, 'generate', '-i', '/local/api/openapi.json', '-g', target,
                '-o', f'/local/raw/{target}', '-c', f'/local/{target}.json',
                '--global-property', 'apiTests=false,modelTests=false,apiDocs=false,modelDocs=false')
        go = stage / 'sdks/go/generated'
        go.mkdir(parents=True)
        for source in (stage / 'raw/go').glob('*.go'):
            shutil.copy(source, go / source.name)
        shutil.copytree(stage / 'raw/python/wave_ai_generated', stage / 'sdks/python/wave_ai_generated')
        shutil.copytree(stage / 'raw/typescript-fetch/src', stage / 'sdks/typescript/src/generated')
        # Node ESM requires explicit extensions. This mechanical normalization
        # is deterministic and does not change generated API behavior.
        import re
        for file in (stage / 'sdks/typescript/src/generated').rglob('*.ts'):
            file.write_text(re.sub(r"(from\s+['\"])(\.[^'\"]+)(['\"])",
                lambda m: m[1] + m[2] + ('' if m[2].endswith('.js') else '.js') + m[3], file.read_text()))
        shutil.copy(specfile, stage / 'sdks/go/openapi.json')
        for destination in ['sdks/go/operations.json', 'sdks/python/wave_ai/operations.json']:
            dump(stage / destination, operations)
        ts = stage / 'sdks/typescript/src/operations.ts'
        ts.write_text('// Generated by tools/clients/generate.py. Do not edit.\nexport const operations = ' +
                      json.dumps(operations, ensure_ascii=False, indent=2) + ' as const;\n' +
                      'export type OperationID = keyof typeof operations;\n')
        reference = stage / 'skills/wave-client/references/commands.md'
        reference.parent.mkdir(parents=True)
        reference.write_text(command_reference(spec, operations))
        dump(stage / 'tools/clients/manifest.json', dict(version=CONFIG['version'],
            sourceSHA256=hashlib.sha256((ROOT / 'internal/platform/apidocs/swagger.json').read_bytes()).hexdigest(),
            openapiSHA256=hashlib.sha256(specfile.read_bytes()).hexdigest(),
            openapiChineseSHA256=hashlib.sha256(chinese.read_bytes()).hexdigest(),
            defaultLanguage='en',
            generatorImage=CONFIG['generatorImage'], operations=len(operations)))
        owned = ['api/openapi.json', 'api/openapi.zh-CN.json', 'sdks/go/generated', 'sdks/go/operations.json', 'sdks/go/openapi.json',
                 'sdks/python/wave_ai_generated', 'sdks/python/wave_ai/operations.json',
                 'sdks/typescript/src/generated', 'sdks/typescript/src/operations.ts',
                 'skills/wave-client/references/commands.md', 'tools/clients/manifest.json']
        changed = []
        for name in owned:
            source, dest = stage / name, ROOT / name
            def files(p):
                if not p.exists(): return {}
                if p.is_file(): return {'': p.read_bytes()}
                return {str(f.relative_to(p)): f.read_bytes() for f in p.rglob('*') if f.is_file()
                        and '__pycache__' not in f.parts}
            if files(source) != files(dest):
                changed.append(name)
                if not args.check:
                    if dest.is_dir(): shutil.rmtree(dest)
                    dest.parent.mkdir(parents=True, exist_ok=True)
                    if source.is_dir(): shutil.copytree(source, dest)
                    else: shutil.copy(source, dest)
        if args.check and changed:
            raise SystemExit('out of date: ' + ', '.join(changed))
        print(f"{'Checked' if args.check else 'Generated'} {len(operations)} operations across three SDKs")

if __name__ == '__main__':
    main()
