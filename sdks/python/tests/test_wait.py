"""Behavior tests for Client/AsyncClient wait(): SSE-first with backoff polling fallback.

Runs against an in-process threaded HTTP server so stream timing, abrupt
disconnects and request counts are observable without the shared Go fixture.
"""
import asyncio
from concurrent.futures import ThreadPoolExecutor
import json
import socket
import struct
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

import pytest

from wave_ai import AsyncClient, Client

INTERVAL = 0.25


def frame(identifier, event, data):
    return f'id: {identifier}\nevent: {event}\ndata: {json.dumps(data)}\n\n'


class FakeWave:
    """Controllable fake service counting every request.

    task_state: callable(count) -> state string, checked on each task GET.
    required_actions: payload returned by the required_actions endpoint.
    stream_plans: list of plans; each plan is a list of steps consumed per
    stream request (the last plan repeats). Steps: ('sleep', s), ('send', text),
    ('abort',) resets the connection, ('end',) closes cleanly.
    """

    def __init__(self):
        self.counts = {'task': 0, 'actions': 0, 'stream': 0}
        self.lock = threading.Lock()
        self.task_state = lambda count: 'running'
        self.required_actions = []
        self.stream_plans = [[]]

    def start(self):
        fake = self

        class Handler(BaseHTTPRequestHandler):
            protocol_version = 'HTTP/1.1'

            def log_message(self, *_):
                pass

            def _json(self, payload, status=200):
                body = json.dumps(payload).encode()
                self.send_response(status)
                self.send_header('Content-Type', 'application/json')
                self.send_header('Content-Length', str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def _reset(self):
                self.connection.setsockopt(socket.SOL_SOCKET, socket.SO_LINGER, struct.pack('ii', 1, 0))
                self.connection.close()

            def do_GET(self):
                path = self.path.split('?')[0]
                try:
                    with fake.lock:
                        if path.startswith('/v1/tasks/'):
                            fake.counts['task'] += 1
                            state = fake.task_state(fake.counts['task'])
                            self._json({'id': 'task_target', 'session_id': 'session_test', 'state': state})
                            return
                        if path.endswith('/required_actions'):
                            fake.counts['actions'] += 1
                            self._json({'data': fake.required_actions})
                            return
                        if path.endswith('/events/stream'):
                            fake.counts['stream'] += 1
                            plan = fake.stream_plans[min(fake.counts['stream'], len(fake.stream_plans)) - 1]
                    self.send_response(200)
                    self.send_header('Content-Type', 'text/event-stream')
                    self.send_header('Connection', 'close')
                    self.end_headers()
                    for step in plan:
                        if step[0] == 'sleep':
                            time.sleep(step[1])
                        elif step[0] == 'send':
                            self.wfile.write(step[1].encode())
                            self.wfile.flush()
                        elif step[0] == 'abort':
                            self._reset()
                            return
                        elif step[0] == 'end':
                            return
                except (BrokenPipeError, ConnectionResetError):
                    pass

        self.httpd = ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        self.httpd.daemon_threads = True
        threading.Thread(target=self.httpd.serve_forever, daemon=True).start()
        self.url = f'http://127.0.0.1:{self.httpd.server_port}'
        return self.url

    def stop(self):
        self.httpd.shutdown()


@pytest.fixture
def fake():
    server = FakeWave()
    server.start()
    yield server
    server.stop()


def sync_wait(fake, **kwargs):
    with Client(fake.url, 'key') as client:
        return client.wait('task_target', **kwargs)


def async_wait(fake, **kwargs):
    async def run():
        async with AsyncClient(fake.url, 'key') as client:
            return await client.wait('task_target', **kwargs)

    return asyncio.run(run())


BOTH = pytest.mark.parametrize('flavor', [sync_wait, async_wait])

# 1. A terminal event on the stream resolves the wait with no polling at all.
@BOTH
def test_terminal_event_no_polling(fake, flavor):
    fake.task_state = lambda count: 'succeeded' if count > 1 else 'running'
    fake.stream_plans = [[('send', frame('1', 'task.succeeded', {'task_id': 'task_target'})), ('end',)]]
    start = time.monotonic()
    result = flavor(fake, timeout=10, interval=INTERVAL)
    assert result.reason == 'terminal' and result.task['state'] == 'succeeded'
    assert fake.counts == {'task': 2, 'actions': 0, 'stream': 1}
    assert time.monotonic() - start < INTERVAL  # no poll sleep happened

# 2. Irrelevant events keep flowing but never wake the wait: the re-check
#    timer is what ends the subscription phase, then a fresh GET resolves.
@BOTH
def test_irrelevant_events_wait_for_timer(fake, flavor):
    noise = [('send', frame(str(i), kind, {'task_id': 'task_target'}))
             for i, kind in enumerate(['message.delta', 'trace.span', 'generation.started',
                                       'agent.created', 'context.compacted', 'message.chunk'])]
    deltas = [step for pair in zip(noise, [('sleep', 0.02)] * len(noise)) for step in pair]
    fake.task_state = lambda count: 'succeeded' if count > 1 else 'running'
    fake.stream_plans = [deltas + [('sleep', 10)]]  # stream stays open after the noise
    start = time.monotonic()
    result = flavor(fake, timeout=10, interval=INTERVAL)
    assert result.reason == 'terminal'
    assert fake.counts == {'task': 2, 'actions': 0, 'stream': 1}
    assert time.monotonic() - start >= INTERVAL  # timer raced, deltas did not wake us

# 3. Deltas arriving on a live stream must not trigger a re-read before the timer.
def test_deltas_do_not_reread(fake):
    fake.stream_plans = [[('send', frame('1', 'message.delta', {'task_id': 'task_target'})), ('sleep', 0.05),
                          ('send', frame('2', 'message.delta', {'task_id': 'task_target'})), ('sleep', 10)]]
    box = {}

    def run():
        with Client(fake.url, 'key') as client:
            box['result'] = client.wait('task_target', timeout=10, interval=0.5)

    with ThreadPoolExecutor(max_workers=1) as pool:
        future = pool.submit(run)
        time.sleep(0.3)  # both deltas delivered, timer not yet elapsed
        assert fake.counts == {'task': 1, 'actions': 0, 'stream': 1}
        fake.task_state = lambda count: 'succeeded' if count > 1 else 'running'
        future.result(timeout=5)
        assert box['result'].reason == 'terminal'
        assert fake.counts['task'] == 2


# 4. A permanently broken stream exhausts its reconnects and falls back to
#    backoff polling, which still observes the terminal state.
@BOTH
def test_stream_break_falls_back_to_polling(fake, flavor):
    fake.stream_plans = [[('send', frame('1', 'message.delta', {'task_id': 'task_target'})), ('abort',)]]
    fake.task_state = lambda count: 'succeeded' if count >= 3 else 'running'
    result = flavor(fake, timeout=15, interval=INTERVAL)
    assert result.reason == 'terminal'
    assert fake.counts['stream'] == 3  # initial attempt + SSE_RECONNECTS
    assert fake.counts['task'] >= 3    # polling took over

# 5. The overall deadline holds even while the stream is open and idle.
@BOTH
def test_wait_timeout(fake, flavor):
    fake.stream_plans = [[('sleep', 10)]]
    start = time.monotonic()
    with pytest.raises(TimeoutError):
        flavor(fake, timeout=0.4, interval=0.1)
    assert time.monotonic() - start < 3

# 6. Cancelling an async wait mid-stream propagates.
def test_async_cancel(fake):
    fake.stream_plans = [[('sleep', 10)]]

    async def run():
        async with AsyncClient(fake.url, 'key') as client:
            task = asyncio.ensure_future(client.wait('task_target', timeout=10, interval=INTERVAL))
            await asyncio.sleep(0.15)
            task.cancel()
            await task

    with pytest.raises(asyncio.CancelledError):
        asyncio.run(run())

# 7. A tool.* event wakes the wait, and the required action is reported.
@BOTH
def test_tool_event_returns_action(fake, flavor):
    # The state stays running: the tool.* trigger forces the required-actions
    # fetch even when the state gate alone would not apply.
    fake.task_state = lambda count: 'running'
    fake.required_actions = [{'task_id': 'task_target', 'type': 'approve_tool', 'call_id': 'call_1'}]
    fake.stream_plans = [[('send', frame('1', 'tool.call', {'task_id': 'task_target'})), ('end',)]]
    result = flavor(fake, timeout=10, interval=INTERVAL)
    assert result.reason == 'action'
    assert [a['call_id'] for a in result.required_actions] == ['call_1']
    assert fake.counts == {'task': 2, 'actions': 1, 'stream': 1}

# 8a. Async: the remaining deadline bounds every attempt without cutting a
#     healthy stream short mid-flight.
def test_async_deadline_midstream(fake):
    deltas = [('send', frame(str(i), 'message.delta', {'task_id': 'task_target'})) for i in range(6)]
    plan = [step for pair in zip(deltas, [('sleep', 0.05)] * 6) for step in pair]
    fake.task_state = lambda count: 'succeeded' if count > 1 else 'running'
    fake.stream_plans = [plan + [('send', frame('9', 'task.succeeded', {'task_id': 'task_target'})), ('end',)]]
    start = time.monotonic()
    result = async_wait(fake, timeout=1.5, interval=0.4)
    assert result.reason == 'terminal'
    assert time.monotonic() - start < 1.5  # stream finished before the deadline

# 8b. Async: near the deadline nothing hangs — requests are bounded by the
#     remaining time and the wait raises TimeoutError promptly.
def test_async_deadline_near_expiry(fake):
    fake.stream_plans = [[('sleep', 10)]]
    fake.task_state = lambda count: 'running'
    start = time.monotonic()
    with pytest.raises(TimeoutError):
        async_wait(fake, timeout=0.4, interval=0.1)
    assert time.monotonic() - start < 3
