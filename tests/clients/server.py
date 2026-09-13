#!/usr/bin/env python3
"""Shared HTTP behavior fixture used by Go, Python, TS and CLI tests."""
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import json
import threading
import time
from urllib.parse import parse_qs, urlsplit

FIXTURES = json.loads(Path(__file__).with_name('fixtures.json').read_text())

class Server(ThreadingHTTPServer):
    daemon_threads = True
    def __init__(self, address):
        super().__init__(address, Handler)
        self.calls, self.lock = {}, threading.Lock()

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_): pass
    def send(self, status, value=None, headers=None):
        body = json.dumps(value, ensure_ascii=False).encode() if value is not None else b''
        self.send_response(status)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(body)))
        for key, value in (headers or {}).items(): self.send_header(key, value)
        self.end_headers()
        if body: self.wfile.write(body)

    def read_body(self):
        if self.headers.get('Transfer-Encoding', '').lower() == 'chunked':
            parts = []
            while True:
                size = int(self.rfile.readline().split(b';')[0], 16)
                if not size:
                    self.rfile.readline()
                    break
                parts.append(self.rfile.read(size)); self.rfile.read(2)
            return b''.join(parts)
        return self.rfile.read(int(self.headers.get('Content-Length', 0)))

    def do_POST(self): self.do_GET()
    def do_PUT(self): self.do_GET()
    def do_DELETE(self): self.do_GET()
    def do_PATCH(self): self.do_GET()
    def do_GET(self):
        try: self.handle_case()
        except (BrokenPipeError, ConnectionResetError): pass

    def handle_case(self):
        parsed = urlsplit(self.path)
        parts = parsed.path.strip('/').split('/')
        if len(parts) < 3: return self.send(404)
        client, case = parts[:2]
        path = '/' + '/'.join(parts[2:])
        key = (client, case, path)
        with self.server.lock:
            count = self.server.calls.get(key, 0)
            self.server.calls[key] = count + 1
        if path == '/stats':
            return self.send(200, {p: n for (c, s, p), n in self.server.calls.items() if c == client and s == case and p != '/stats'})
        if self.headers.get('Authorization') != 'Bearer ' + FIXTURES['key']:
            return self.send(401, FIXTURES['error'])
        query = parse_qs(parsed.query)
        body = self.read_body() if self.command != 'GET' else b''
        if case == 'error': return self.send(403, FIXTURES['error'])
        if case == 'retry' or case.startswith('no-retry'):
            if case == 'retry' and (not self.headers.get('Idempotency-Key') or json.loads(body) != {'agent_id': 'agent_test', 'input': 'hello'}):
                return self.send(422, {'error': {'message': 'idempotency body changed'}})
            return self.send(503 if count == 0 else 202, FIXTURES['task'], {'Retry-After': '0'})
        if case in ('approve', 'empty'):
            expected = FIXTURES['approval' if case == 'approve' else 'emptyResult']
            return self.send(202 if json.loads(body) == expected else 422)
        if case in ('offset', 'after', 'sequence', 'stuck'):
            cursor = query.get('offset' if case in ('offset', 'stuck') else 'after', ['0'])[0]
            if case == 'stuck': return self.send(200, {'data': [{'id': 'a'}], 'next_offset': 0})
            if cursor == '0':
                page = {'data': [{'id': 'a', 'sequence': 2}]}
                if case == 'offset': page['next_offset'] = 2
                if case == 'after': page['next_after'] = 2
            else: page = {'data': []}
            return self.send(200, page)
        if case.startswith('wait'):
            if path == '/v1/tasks/task_target':
                task = dict(FIXTURES['task'])
                if case == 'wait-child' and count > 0: task['state'] = 'succeeded'
                return self.send(200, task)
            action = {'task_id': 'task_target' if case == 'wait-action' else 'task_child', 'type': 'approve_tool', 'call_id': 'call_test'}
            return self.send(200, {'data': [action]})
        if case in ('sse', 'reconnect'):
            expected = '42' if count == 0 else '44'
            if 'after' in query or self.headers.get('Last-Event-ID') != expected:
                return self.send(422, {'error': {'message': 'wrong resume cursor'}})
            self.send_response(200); self.send_header('Content-Type', 'text/event-stream'); self.end_headers()
            if count == 0:
                payload = '\ufeff: heartbeat\r\nid: 43\r\nevent: task.finished\r\ndata: {"task_id":"task_child"}\r\n\r\nid: 44\nevent: custom.future\ndata: {"task_id": "task_target",\ndata: "text": "中文"}\n\n'
            else: payload = 'id: 45\nevent: task.finished\ndata: {"task_id":"task_target"}\n\n'
            data = payload.encode()
            for index in range(0, len(data), 7):
                self.wfile.write(data[index:index+7]); self.wfile.flush()
            if case == 'sse': time.sleep(2) # Clients must yield before this finishes.
            return
        if case in ('download', 'range', 'not-modified', 'range-error', 'json-file'):
            data = FIXTURES['binary'].encode()
            status = 200
            if case == 'range':
                if self.headers.get('Range') != 'bytes=0-3': return self.send(422)
                data, status = data[:4], 206
            if case == 'not-modified': return self.send(304)
            if case == 'json-file': return self.send(200, FIXTURES['error'])
            if case == 'range-error': data, status = b'invalid range', 416
            self.send_response(status); self.send_header('Content-Type', 'text/plain' if status == 416 else 'application/octet-stream')
            self.send_header('Content-Length', str(len(data)))
            if status == 206: self.send_header('Content-Range', 'bytes 0-3/28')
            self.end_headers(); self.wfile.write(data); return
        if case == 'upload':
            if not self.headers.get('Content-Type', '').startswith('multipart/form-data;') or FIXTURES['binary'].encode() not in body:
                return self.send(422, {'error': {'message': 'invalid multipart upload'}})
            return self.send(201, {'id': 'file_test'})
        return self.send(200, {'data': [], 'usage': FIXTURES['usage']})

if __name__ == '__main__':
    import sys
    server = Server(('127.0.0.1', 0))
    Path(sys.argv[1]).write_text(f'http://127.0.0.1:{server.server_port}')
    server.serve_forever()
