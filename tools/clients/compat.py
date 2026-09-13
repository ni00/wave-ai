#!/usr/bin/env python3
"""Compare a previous contract and command map before release."""
import argparse
import json
from pathlib import Path
import subprocess

ROOT=Path(__file__).resolve().parents[2]
parser=argparse.ArgumentParser()
parser.add_argument('previous_spec',type=Path)
parser.add_argument('previous_operations',type=Path)
args=parser.parse_args()
old=json.loads(args.previous_operations.read_text())
current=json.loads((ROOT/'tools/clients/operations.json').read_text())
changes=[]
for id,op in old.items():
    if id not in current: changes.append(f'removed operation {id}')
    elif op['command']!=current[id]['command']: changes.append(f'renamed CLI command {op["command"]}')
subprocess.run(['go','run','github.com/oasdiff/oasdiff@v1.31.0','breaking',str(args.previous_spec),str(ROOT/'api/openapi.json'),'--fail-on','WARN'],check=True)
if changes: raise SystemExit('\n'.join(changes))
print('HTTP contract and CLI operation compatibility passed')
