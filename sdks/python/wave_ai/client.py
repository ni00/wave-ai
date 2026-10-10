from __future__ import annotations

import asyncio
from dataclasses import dataclass
from importlib.resources import files
import json
import random
import time
from typing import Any, AsyncIterator, BinaryIO, Iterator
from urllib.parse import quote, urlsplit

import httpx

OPERATIONS = json.loads(files(__package__).joinpath('operations.json').read_text())
RETRY_STATUS = {429, 502, 503, 504}
TERMINAL = {'succeeded', 'partial', 'failed', 'canceled'}
WAIT_EVENTS = frozenset({'agent.wait', 'generation.failed', 'generation.interrupted', 'cancel.requested',
                         'budget.exhausted', 'input.accepted', 'input.consumed'})
SSE_RECONNECTS = 2

class APIError(Exception):
    def __init__(self, status: int, code: str, message: str, request_id: str = ''):
        self.status, self.code, self.message, self.request_id = status, code, message, request_id
        super().__init__(f'Wave HTTP {status} {code}: {message} (request_id={request_id})')


@dataclass
class Response:
    status: int
    headers: httpx.Headers
    data: Any = None


@dataclass
class Event:
    id: str
    type: str
    data: str


@dataclass
class WaitResult:
    task: dict[str, Any]
    required_actions: list[dict[str, Any]]
    reason: str


def _error(response: httpx.Response, data: bytes) -> APIError:
    try:
        envelope = json.loads(data)
        error = envelope.get('error', {})
        return APIError(response.status_code, error.get('type', ''), error.get('message', response.reason_phrase),
                        envelope.get('request_id', response.headers.get('x-request-id', '')))
    except (ValueError, AttributeError):
        return APIError(response.status_code, '', data.decode('utf-8', errors='replace') or response.reason_phrase,
                        response.headers.get('x-request-id', ''))


def _tool(body: Any) -> None:
    if not isinstance(body, dict):
        raise ValueError('tool result body must be an object')
    approve, result = body.get('approve'), body.get('result')
    if (approve is None) == (result is None):
        raise ValueError('submit exactly one non-null approve or result')
    if approve is not None and not isinstance(approve, bool):
        raise ValueError('approve must be boolean')
    if result is not None and not isinstance(result, str):
        raise ValueError('result must be a string')
    if body.get('error_code') and not body.get('is_error'):
        raise ValueError('error_code requires is_error=true')
    if approve is not None and (body.get('is_error') or body.get('error_code')):
        raise ValueError('approval cannot include error details')


def _wait_wake(event: Event, task_id: str) -> bool:
    """True when a stream event requires re-reading the task. Session-wide
    events without a task_id wake the caller as well."""
    if event.type not in WAIT_EVENTS and not event.type.startswith(('task.', 'tool.')):
        return False
    try:
        return json.loads(event.data).get('task_id', task_id) == task_id
    except ValueError:
        return False


def _wait_poll_delay(attempt: int, interval: float) -> float:
    """Exponential backoff with jitter, capped at the poll interval."""
    return min(interval, 0.2 * 2 ** min(attempt, 5)) * (0.5 + random.random())


class _SSE:
    def __init__(self):
        self.id, self.kind, self.data, self.size, self.first = '', '', [], 0, True

    def feed(self, line: str) -> Event | None:
        if self.first:
            line = line.removeprefix('\ufeff')
            self.first = False
        if not line:
            event = Event(self.id, self.kind or 'message', '\n'.join(self.data)) if self.data else None
            self.kind, self.data, self.size = '', [], 0
            return event
        field, _, value = line.partition(':')
        value = value.removeprefix(' ')
        if field == 'id' and '\0' not in value:
            self.id = value
        elif field == 'event':
            self.kind = value
        elif field == 'data':
            self.size += len(value.encode('utf-8'))
            if self.size > 1024 * 1024:
                raise ValueError('SSE event exceeds 1 MiB')
            self.data.append(value)
        return None


def _next(op: dict, query: dict, page: dict) -> str | None:
    pagination = op['pagination']
    items = page['data']
    if not items:
        return None
    value = items[-1].get(pagination['itemCursor']) if 'itemCursor' in pagination else page.get(pagination['next'])
    if value is None:
        return None
    if not isinstance(value, int) or isinstance(value, bool) or value <= int(query.get(pagination['request'], 0)):
        raise ValueError('pagination cursor did not advance')
    return str(value)


def _delay(response: httpx.Response | None, attempt: int) -> float:
    delay = 0.2 * 2 ** min(attempt, 5)
    if response is not None:
        try:
            delay = float(response.headers['retry-after'])
        except (KeyError, ValueError):
            pass
    return min(30, max(0, delay))


class _Base:
    def __init__(self, base_url: str, api_key: str, max_retries: int):
        if not isinstance(max_retries, int) or max_retries < 0:
            raise ValueError('max_retries must be a non-negative integer')
        url = urlsplit(base_url)
        if url.scheme not in ('http', 'https') or not url.netloc or url.username or url.password or url.query or url.fragment:
            raise ValueError('base_url must be an HTTP(S) URL without credentials, query or fragment')
        self.base_url = base_url.rstrip('/')
        api_key = api_key.strip()
        self.api_key = api_key[7:].strip() if api_key.lower().startswith('bearer ') else api_key
        self.max_retries = max_retries

    def _request(self, operation: str, path=None, query=None, headers=None):
        op = OPERATIONS[operation]
        target = op['path']
        path = path or {}
        for param in op['parameters']:
            if param['in'] == 'path':
                value = path.get(param['name'])
                if not value or value in ('.', '..'):
                    raise ValueError(f"missing or invalid path parameter {param['name']}")
                target = target.replace('{' + param['name'] + '}', quote(str(value), safe=''))
        headers = httpx.Headers(headers or {})
        headers['User-Agent'] = 'wave-python/0.1.0'
        if op['authenticated'] and self.api_key:
            headers['Authorization'] = 'Bearer ' + self.api_key
        return op, dict(method=op['method'], url=self.base_url + target, params=query, headers=headers)

    def _call(self, operation, path, query, headers, body):
        op, request = self._request(operation, path, query, headers)
        if op.get('transport'):
            raise ValueError(f"{operation} requires the {op['transport']} API")
        if (op.get('requestBody') or {}).get('required') and body is None:
            raise ValueError('request body is required')
        if operation == 'executionResolveToolResult':
            _tool(body)
        if body is not None:
            request['content'] = json.dumps(body, ensure_ascii=False, separators=(',', ':'), allow_nan=False).encode()
            request['headers']['Content-Type'] = 'application/json'
        safe = op['method'] == 'GET' or (op.get('idempotent', False) and bool(request['headers'].get('Idempotency-Key')))
        return request, safe


class Client(_Base):
    def __init__(self, base_url='http://localhost:8080', api_key='', *, timeout=30.0, max_retries=2, http_client=None):
        super().__init__(base_url, api_key, max_retries)
        self.http = http_client or httpx.Client(timeout=timeout)
        self._owned = http_client is None

    def close(self):
        if self._owned:
            self.http.close()

    def __enter__(self): return self
    def __exit__(self, *_): self.close()

    def call(self, operation: str, *, path=None, query=None, headers=None, body=None, timeout=None) -> Response:
        request, safe = self._call(operation, path, query, headers, body)
        deadline = time.monotonic() + timeout if timeout is not None else None
        for attempt in range(self.max_retries + 1):
            if deadline is not None:
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise TimeoutError('request timed out')
                request['timeout'] = remaining
            response = None
            try:
                response = self.http.request(**request)
            except httpx.TransportError:
                if not safe or attempt == self.max_retries:
                    raise
            else:
                if response.status_code not in RETRY_STATUS or not safe or attempt == self.max_retries:
                    if not 200 <= response.status_code < 300:
                        raise _error(response, response.content[:1024*1024])
                    return Response(response.status_code, response.headers, response.json() if response.content else None)
            delay = _delay(response, attempt)
            if deadline is not None:
                delay = min(delay, max(0, deadline - time.monotonic()))
            time.sleep(delay)
        raise RuntimeError('invalid retry configuration')

    def each(self, operation: str, *, path=None, query=None, headers=None) -> Iterator[Any]:
        op = OPERATIONS[operation]
        if 'pagination' not in op:
            raise ValueError('operation has no pagination')
        query = dict(query or {})
        key = op['pagination']['request']
        query.setdefault(key, httpx.Headers(headers or {}).get('Last-Event-ID', '0'))
        while True:
            page = self.call(operation, path=path, query=query, headers=headers).data
            yield from page['data']
            cursor = _next(op, query, page)
            if cursor is None:
                return
            query[key] = cursor

    def events(self, session: str, *, after=None, last_event_id=None, task_id=None, max_reconnects=0) -> Iterator[Event]:
        if not isinstance(max_reconnects, int) or max_reconnects < 0:
            raise ValueError('max_reconnects must be a non-negative integer')
        if after is not None and last_event_id is not None:
            raise ValueError('provide after or last_event_id, not both')
        cursor = last_event_id
        for attempt in range(max_reconnects + 1):
            headers = {'Accept': 'text/event-stream'}
            query = {}
            if cursor is not None:
                headers['Last-Event-ID'] = str(cursor)
            elif after is not None:
                query['after'] = str(after)
            _, request = self._request('executionStreamEvents', {'id': session}, query, headers)
            try:
                with self.http.stream(**request, timeout=httpx.Timeout(None, connect=10)) as response:
                    if response.status_code != 200:
                        raise _error(response, response.read()[:1024*1024])
                    if response.headers.get('content-type', '').split(';')[0].strip() != 'text/event-stream':
                        raise ValueError('expected text/event-stream')
                    parser = _SSE()
                    for line in response.iter_lines():
                        event = parser.feed(line)
                        if event:
                            if task_id is None or json.loads(event.data).get('task_id') == task_id:
                                yield event
                            cursor = event.id
            except APIError as error:
                if error.status not in RETRY_STATUS or attempt == max_reconnects:
                    raise
            except httpx.TransportError:
                if attempt == max_reconnects:
                    raise
            if attempt < max_reconnects:
                time.sleep(_delay(None, attempt))

    def _wait_probe(self, task_id: str, task: dict[str, Any], deadline: float, trigger: str) -> WaitResult | None:
        """Check authoritative task state after a read. Required actions are
        fetched when the state is waiting/unknown, or unconditionally when a
        tool.* or agent.wait event triggered the re-read."""
        if task['state'] in TERMINAL:
            return WaitResult(task, [], 'terminal')
        if not (task['state'] in ('waiting', 'unknown') or trigger.startswith('tool.') or trigger == 'agent.wait'):
            return None
        actions = self.call('executionRequiredActions', path={'id': task['session_id']},
                            timeout=max(0.001, deadline - time.monotonic())).data['data']
        actions = [a for a in actions if a['task_id'] == task_id]
        if actions or task['state'] == 'unknown':
            return WaitResult(task, actions, 'action')
        return None

    def _wait_subscribe(self, session: str, task_id: str, deadline: float, interval: float) -> str:
        """Watch the session stream for a relevant event, racing a re-check
        timer. Returns the waking event type (or 'timer' when the window
        elapsed), or 'broken' when the stream is unusable and polling must
        take over."""
        cursor, attempt = None, 0
        while True:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                return 'timer'
            headers = {'Accept': 'text/event-stream'}
            if cursor is not None:
                headers['Last-Event-ID'] = str(cursor)
            _, request = self._request('executionStreamEvents', {'id': session}, None, headers)
            window = max(0.001, min(interval, remaining))
            try:
                with self.http.stream(**request,
                                      timeout=httpx.Timeout(window, connect=max(0.001, min(10, remaining)))) as response:
                    if response.status_code != 200:
                        raise _error(response, response.read()[:1024*1024])
                    if response.headers.get('content-type', '').split(';')[0].strip() != 'text/event-stream':
                        raise ValueError('expected text/event-stream')
                    parser, wake_at = _SSE(), time.monotonic() + window
                    for line in response.iter_lines():
                        event = parser.feed(line)
                        if event:
                            cursor = event.id
                            if _wait_wake(event, task_id):
                                return event.type
                        if time.monotonic() >= wake_at:
                            return 'timer'
            except httpx.ReadTimeout:
                return 'timer'
            except httpx.TransportError:
                pass
            except APIError as error:
                if error.status not in RETRY_STATUS:
                    raise
            if attempt >= SSE_RECONNECTS:
                return 'broken'
            attempt += 1
            time.sleep(min(0.2 * 2 ** min(attempt, 5), max(0, deadline - time.monotonic())))

    def wait(self, task_id: str, *, timeout=300.0, interval=1.0) -> WaitResult:
        deadline = time.monotonic() + timeout
        trigger = ''
        while True:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise TimeoutError('task wait timed out')
            task = self.call('executionGetTask', path={'id': task_id}, timeout=remaining).data
            result = self._wait_probe(task_id, task, deadline, trigger)
            if result:
                return result
            trigger = self._wait_subscribe(task['session_id'], task_id, deadline, interval)
            if trigger == 'broken':
                break
        attempt = 0
        while True:  # stream unusable: fall back to backoff polling
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise TimeoutError('task wait timed out')
            task = self.call('executionGetTask', path={'id': task_id}, timeout=remaining).data
            result = self._wait_probe(task_id, task, deadline, '')
            if result:
                return result
            attempt += 1
            time.sleep(min(_wait_poll_delay(attempt, interval), max(0, deadline - time.monotonic())))

    def approve(self, task: str, call: str, approve: bool):
        return self.call('executionResolveToolResult', path={'id': task, 'call': call}, body={'approve': approve})

    def submit_result(self, task: str, call: str, result: str, *, is_error=False, error_code=''):
        return self.call('executionResolveToolResult', path={'id': task, 'call': call},
                         body={'result': result, 'is_error': is_error, 'error_code': error_code})

    def download(self, operation: str, id: str, destination: BinaryIO, *, headers=None) -> Response:
        op, request = self._request(operation, {'id': id}, headers=headers)
        if op.get('transport') != 'download':
            raise ValueError('not a download operation')
        with self.http.stream(**request) as response:
            if response.status_code == 304:
                return Response(304, response.headers)
            if response.status_code not in (200, 206):
                raise _error(response, response.read()[:1024*1024])
            if response.headers.get('content-type', '').startswith('application/json'):
                raise ValueError('refusing JSON in a binary download')
            size = 0
            for chunk in response.iter_bytes():
                destination.write(chunk)
                size += len(chunk)
            return Response(response.status_code, response.headers, {'bytes': size})

    def upload(self, operation: str, filename: str, source: BinaryIO, *, fields=None) -> Response:
        op, request = self._request(operation)
        if op.get('transport') != 'upload':
            raise ValueError('not an upload operation')
        response = self.http.request(**request, files={'file': (filename, source)}, data=fields)
        if not 200 <= response.status_code < 300:
            raise _error(response, response.content[:1024*1024])
        return Response(response.status_code, response.headers, response.json())


class AsyncClient(_Base):
    def __init__(self, base_url='http://localhost:8080', api_key='', *, timeout=30.0, max_retries=2, http_client=None):
        super().__init__(base_url, api_key, max_retries)
        self.http = http_client or httpx.AsyncClient(timeout=timeout)
        self._owned = http_client is None

    async def close(self):
        if self._owned:
            await self.http.aclose()

    async def __aenter__(self): return self
    async def __aexit__(self, *_): await self.close()

    async def call(self, operation: str, *, path=None, query=None, headers=None, body=None, timeout=None) -> Response:
        request, safe = self._call(operation, path, query, headers, body)
        if timeout is not None:
            request['timeout'] = timeout
        for attempt in range(self.max_retries + 1):
            response = None
            try:
                response = await self.http.request(**request)
            except httpx.TransportError:
                if not safe or attempt == self.max_retries:
                    raise
            else:
                if response.status_code not in RETRY_STATUS or not safe or attempt == self.max_retries:
                    if not 200 <= response.status_code < 300:
                        raise _error(response, response.content[:1024*1024])
                    return Response(response.status_code, response.headers, response.json() if response.content else None)
            await asyncio.sleep(_delay(response, attempt))
        raise RuntimeError('invalid retry configuration')

    async def each(self, operation: str, *, path=None, query=None, headers=None) -> AsyncIterator[Any]:
        op = OPERATIONS[operation]
        if 'pagination' not in op:
            raise ValueError('operation has no pagination')
        query = dict(query or {})
        key = op['pagination']['request']
        query.setdefault(key, httpx.Headers(headers or {}).get('Last-Event-ID', '0'))
        while True:
            page = (await self.call(operation, path=path, query=query, headers=headers)).data
            for item in page['data']:
                yield item
            cursor = _next(op, query, page)
            if cursor is None:
                return
            query[key] = cursor

    async def events(self, session: str, *, after=None, last_event_id=None, task_id=None, max_reconnects=0) -> AsyncIterator[Event]:
        if not isinstance(max_reconnects, int) or max_reconnects < 0:
            raise ValueError('max_reconnects must be a non-negative integer')
        if after is not None and last_event_id is not None:
            raise ValueError('provide after or last_event_id, not both')
        cursor = last_event_id
        for attempt in range(max_reconnects + 1):
            headers = {'Accept': 'text/event-stream'}
            query = {}
            if cursor is not None:
                headers['Last-Event-ID'] = str(cursor)
            elif after is not None:
                query['after'] = str(after)
            _, request = self._request('executionStreamEvents', {'id': session}, query, headers)
            try:
                async with self.http.stream(**request, timeout=httpx.Timeout(None, connect=10)) as response:
                    if response.status_code != 200:
                        raise _error(response, (await response.aread())[:1024*1024])
                    if response.headers.get('content-type', '').split(';')[0].strip() != 'text/event-stream':
                        raise ValueError('expected text/event-stream')
                    parser = _SSE()
                    async for line in response.aiter_lines():
                        event = parser.feed(line)
                        if event:
                            if task_id is None or json.loads(event.data).get('task_id') == task_id:
                                yield event
                            cursor = event.id
            except APIError as error:
                if error.status not in RETRY_STATUS or attempt == max_reconnects:
                    raise
            except httpx.TransportError:
                if attempt == max_reconnects:
                    raise
            if attempt < max_reconnects:
                await asyncio.sleep(_delay(None, attempt))

    async def _wait_probe(self, task_id: str, task: dict[str, Any], deadline: float, trigger: str) -> WaitResult | None:
        """Check authoritative task state after a read. Required actions are
        fetched when the state is waiting/unknown, or unconditionally when a
        tool.* or agent.wait event triggered the re-read. Every HTTP attempt
        carries the remaining deadline."""
        if task['state'] in TERMINAL:
            return WaitResult(task, [], 'terminal')
        if not (task['state'] in ('waiting', 'unknown') or trigger.startswith('tool.') or trigger == 'agent.wait'):
            return None
        actions = (await self.call('executionRequiredActions', path={'id': task['session_id']},
                                   timeout=max(0.001, deadline - time.monotonic()))).data['data']
        actions = [a for a in actions if a['task_id'] == task_id]
        if actions or task['state'] == 'unknown':
            return WaitResult(task, actions, 'action')
        return None

    async def _wait_subscribe(self, session: str, task_id: str, deadline: float, interval: float) -> str:
        """Watch the session stream for a relevant event, racing a re-check
        timer. Returns the waking event type (or 'timer' when the window
        elapsed), or 'broken' when the stream is unusable and polling must
        take over. Every attempt is bounded by the remaining deadline."""
        cursor, attempt = None, 0
        while True:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                return 'timer'
            headers = {'Accept': 'text/event-stream'}
            if cursor is not None:
                headers['Last-Event-ID'] = str(cursor)
            _, request = self._request('executionStreamEvents', {'id': session}, None, headers)
            window = max(0.001, min(interval, remaining))
            try:
                async with self.http.stream(**request,
                                            timeout=httpx.Timeout(window, connect=max(0.001, min(10, remaining)))) as response:
                    if response.status_code != 200:
                        raise _error(response, (await response.aread())[:1024*1024])
                    if response.headers.get('content-type', '').split(';')[0].strip() != 'text/event-stream':
                        raise ValueError('expected text/event-stream')
                    parser, wake_at = _SSE(), time.monotonic() + window
                    async for line in response.aiter_lines():
                        event = parser.feed(line)
                        if event:
                            cursor = event.id
                            if _wait_wake(event, task_id):
                                return event.type
                        if time.monotonic() >= wake_at:
                            return 'timer'
            except httpx.ReadTimeout:
                return 'timer'
            except httpx.TransportError:
                pass
            except APIError as error:
                if error.status not in RETRY_STATUS:
                    raise
            if attempt >= SSE_RECONNECTS:
                return 'broken'
            attempt += 1
            await asyncio.sleep(min(0.2 * 2 ** min(attempt, 5), max(0, deadline - time.monotonic())))

    async def wait(self, task_id: str, *, timeout=300.0, interval=1.0) -> WaitResult:
        deadline = time.monotonic() + timeout
        async with _deadline(timeout):
            trigger = ''
            while True:
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise TimeoutError('task wait timed out')
                task = (await self.call('executionGetTask', path={'id': task_id}, timeout=remaining)).data
                result = await self._wait_probe(task_id, task, deadline, trigger)
                if result:
                    return result
                trigger = await self._wait_subscribe(task['session_id'], task_id, deadline, interval)
                if trigger == 'broken':
                    break
            attempt = 0
            while True:  # stream unusable: fall back to backoff polling
                remaining = deadline - time.monotonic()
                if remaining <= 0:
                    raise TimeoutError('task wait timed out')
                task = (await self.call('executionGetTask', path={'id': task_id}, timeout=remaining)).data
                result = await self._wait_probe(task_id, task, deadline, '')
                if result:
                    return result
                attempt += 1
                await asyncio.sleep(min(_wait_poll_delay(attempt, interval), max(0, deadline - time.monotonic())))

    async def approve(self, task: str, call: str, approve: bool):
        return await self.call('executionResolveToolResult', path={'id': task, 'call': call}, body={'approve': approve})

    async def submit_result(self, task: str, call: str, result: str, *, is_error=False, error_code=''):
        return await self.call('executionResolveToolResult', path={'id': task, 'call': call},
                               body={'result': result, 'is_error': is_error, 'error_code': error_code})

    async def download(self, operation: str, id: str, destination: BinaryIO, *, headers=None) -> Response:
        op, request = self._request(operation, {'id': id}, headers=headers)
        if op.get('transport') != 'download':
            raise ValueError('not a download operation')
        async with self.http.stream(**request) as response:
            if response.status_code == 304:
                return Response(304, response.headers)
            if response.status_code not in (200, 206):
                raise _error(response, (await response.aread())[:1024*1024])
            if response.headers.get('content-type', '').startswith('application/json'):
                raise ValueError('refusing JSON in a binary download')
            size = 0
            async for chunk in response.aiter_bytes():
                await asyncio.to_thread(destination.write, chunk)
                size += len(chunk)
            return Response(response.status_code, response.headers, {'bytes': size})

    async def upload(self, operation: str, filename: str, source: BinaryIO, *, fields=None) -> Response:
        op, request = self._request(operation)
        if op.get('transport') != 'upload':
            raise ValueError('not an upload operation')
        response = await self.http.request(**request, files={'file': (filename, source)}, data=fields)
        if not 200 <= response.status_code < 300:
            raise _error(response, response.content[:1024*1024])
        return Response(response.status_code, response.headers, response.json())


class _deadline:
    """Cancellation-based timeout compatible with Python 3.10."""
    def __init__(self, seconds): self.seconds, self.expired = seconds, False
    async def __aenter__(self):
        self.task = asyncio.current_task()
        def cancel():
            self.expired = True
            self.task.cancel()
        self.handle = asyncio.get_running_loop().call_later(self.seconds, cancel)
    async def __aexit__(self, kind, value, traceback):
        self.handle.cancel()
        if kind is asyncio.CancelledError and self.expired:
            if hasattr(self.task, 'uncancel'):
                self.task.uncancel()
            raise TimeoutError('task wait timed out') from value
