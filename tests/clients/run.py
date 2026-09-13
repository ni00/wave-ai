#!/usr/bin/env python3
"""Run all client behavior suites against the same isolated HTTP fixture."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import threading
from server import Server

ROOT = Path(__file__).resolve().parents[2]
server = Server(('127.0.0.1', 0))
threading.Thread(target=server.serve_forever, daemon=True).start()
env = dict(os.environ, WAVE_CLIENT_TEST_URL=f'http://127.0.0.1:{server.server_port}')
try:
    for directory, command in [
        ('sdks/go', ['go', 'test', '-race', './...']),
        ('sdks/python', ['uv', 'run', '--frozen', 'pytest', '-q']),
        ('sdks/typescript', ['npm', 'test']),
        ('cli', ['go', 'test', '-race', './...']),
    ]:
        print(f'Running {directory}', flush=True)
        subprocess.run(command, cwd=ROOT / directory, env=env, check=True)
finally:
    server.shutdown()
    server.server_close()
