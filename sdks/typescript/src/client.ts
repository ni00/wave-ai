import { operations, OperationID } from './operations.js';
import { Configuration } from './generated/runtime.js';

type JSONRecord = Record<string, any>;
interface Operation {
  method: string; path: string; authenticated: boolean; parameters: readonly {name: string; in: string}[];
  transport?: string; idempotent?: boolean; requestBody?: {required?: boolean} | null;
  pagination?: {request: string; next?: string; itemCursor?: string};
}
const registry: Record<string, Operation> = operations;
const retryStatus = new Set([429, 502, 503, 504]);
const terminal = new Set(['succeeded', 'partial', 'failed', 'canceled']);

export interface Options {
  path?: Record<string, string>;
  query?: Record<string, string | number | boolean | undefined>;
  headers?: HeadersInit;
  body?: unknown;
  signal?: AbortSignal;
}
export interface APIResponse<T = unknown> { status: number; headers: Headers; data: T }
export interface Event { id: string; type: string; data: string }
export interface WaitResult { task: JSONRecord; required_actions: JSONRecord[]; reason: 'terminal' | 'action' }
export interface StreamOptions { after?: string; lastEventID?: string; taskID?: string; maxReconnects?: number; signal?: AbortSignal }

export class APIError extends Error {
  constructor(public status: number, public code: string, message: string, public requestID: string = '') {
    super(`Wave HTTP ${status} ${code}: ${message} (request_id=${requestID})`);
    this.name = 'APIError';
  }
}

// int64 values beyond JS's exact range are rejected, never silently rounded.
export function parseJSON(text: string): any {
  return JSON.parse(text, (_key, value) => {
    if (typeof value === 'number' && Number.isInteger(value) && !Number.isSafeInteger(value))
      throw new RangeError('Wave integer exceeds JavaScript safe integer range');
    return value;
  });
}
function encodeJSON(value: unknown): string {
  return JSON.stringify(value, (_key, item) => {
    if (typeof item === 'number' && (!Number.isFinite(item) || (Number.isInteger(item) && !Number.isSafeInteger(item))))
      throw new RangeError('invalid or unsafe JSON number');
    return item;
  });
}
async function apiError(response: Response): Promise<APIError> {
  const text = await response.text();
  try {
    const value = JSON.parse(text);
    return new APIError(response.status, value.error?.type ?? '', value.error?.message ?? response.statusText,
      value.request_id ?? response.headers.get('x-request-id') ?? '');
  } catch {
    return new APIError(response.status, '', text.slice(0, 1024 * 1024) || response.statusText, response.headers.get('x-request-id') ?? '');
  }
}
function tool(body: unknown) {
  if (!body || typeof body !== 'object') throw new TypeError('tool body must be an object');
  const value = body as JSONRecord;
  const a = value.approve, r = value.result;
  if ((a == null) === (r == null)) throw new TypeError('submit exactly one non-null approve or result');
  if (a != null && typeof a !== 'boolean') throw new TypeError('approve must be boolean');
  if (r != null && typeof r !== 'string') throw new TypeError('result must be a string');
  if (value.error_code && !value.is_error) throw new TypeError('error_code requires is_error=true');
  if (a != null && (value.is_error || value.error_code)) throw new TypeError('approval cannot include error details');
}
function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  signal?.throwIfAborted();
  return new Promise((resolve, reject) => {
    const abort = () => { clearTimeout(timer); reject(signal!.reason); };
    const timer = setTimeout(() => { signal?.removeEventListener('abort', abort); resolve(); }, ms);
    signal?.addEventListener('abort', abort, {once: true});
  });
}
function delay(response: Response | undefined, attempt: number): number {
  const retry = response?.headers.get('retry-after');
  const seconds = retry == null ? NaN : Number(retry);
  return Math.max(0, Math.min(30000, Number.isFinite(seconds) ? seconds * 1000 : 200 * 2 ** Math.min(attempt, 5)));
}

class SSEParser {
  private id = ''; private kind = ''; private data: string[] = []; private size = 0;
  feed(line: string): Event | undefined {
    if (!line) {
      const result = this.data.length ? {id: this.id, type: this.kind || 'message', data: this.data.join('\n')} : undefined;
      this.kind = ''; this.data = []; this.size = 0;
      return result;
    }
    const colon = line.indexOf(':');
    const field = colon < 0 ? line : line.slice(0, colon);
    let value = colon < 0 ? '' : line.slice(colon + 1);
    if (value.startsWith(' ')) value = value.slice(1);
    if (field === 'id' && !value.includes('\0')) this.id = value;
    if (field === 'event') this.kind = value;
    if (field === 'data') {
      this.size += new TextEncoder().encode(value).length;
      if (this.size > 1024 * 1024) throw new Error('SSE event exceeds 1 MiB');
      this.data.push(value);
    }
    return undefined;
  }
}
async function* lines(body: ReadableStream<Uint8Array>): AsyncGenerator<string> {
  const reader = body.getReader(); const decoder = new TextDecoder(); let buffer = '';
  try {
    while (true) {
      const {value, done} = await reader.read(); buffer += decoder.decode(value, {stream: !done});
      while (true) {
        const match = /[\r\n]/.exec(buffer);
        if (!match) break;
        const i = match.index;
        if (buffer[i] === '\r' && i + 1 === buffer.length && !done) break;
        const count = buffer[i] === '\r' && buffer[i+1] === '\n' ? 2 : 1;
        const line = buffer.slice(0, i); buffer = buffer.slice(i + count); yield line;
      }
      if (buffer.length > 1024 * 1024) throw new Error('SSE line exceeds 1 MiB');
      if (done) return; // Discard an incomplete frame at EOF.
    }
  } finally { await reader.cancel(); reader.releaseLock(); }
}

export class Client {
  readonly baseURL: string;
  readonly apiKey: string;
  readonly fetch: typeof globalThis.fetch;
  readonly maxRetries: number;
  constructor({baseURL = 'http://localhost:8080', apiKey = '', fetch = globalThis.fetch, maxRetries = 2}:
    {baseURL?: string; apiKey?: string; fetch?: typeof globalThis.fetch; maxRetries?: number} = {}) {
    const url = new URL(baseURL);
    if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash)
      throw new TypeError('baseURL must be an HTTP(S) URL without credentials, query or fragment');
    this.baseURL = baseURL.replace(/\/+$/, ''); this.apiKey = apiKey.trim().replace(/^bearer\s+/i, '');
    if (!Number.isInteger(maxRetries) || maxRetries < 0) throw new TypeError('maxRetries must be a non-negative integer');
    this.fetch = fetch; this.maxRetries = maxRetries;
  }
  /** Configuration for every typed generated API class. */
  rawConfiguration(): Configuration { return new Configuration({basePath: this.baseURL, accessToken: this.apiKey, fetchApi: this.fetch}); }
  private request(operation: OperationID, options: Options = {}): [Operation, string, RequestInit] {
    const op = registry[operation]; if (!op) throw new TypeError(`unknown operation: ${operation}`);
    let path = op.path;
    for (const p of op.parameters) if (p.in === 'path') {
      const value = options.path?.[p.name];
      if (!value || value === '.' || value === '..') throw new TypeError(`missing or invalid path parameter ${p.name}`);
      path = path.replace(`{${p.name}}`, encodeURIComponent(value));
    }
    const url = new URL(this.baseURL + path);
    for (const [key, value] of Object.entries(options.query ?? {})) if (value !== undefined) {
      if (typeof value === 'number' && !Number.isSafeInteger(value)) throw new TypeError('query numbers must be safe integers');
      url.searchParams.set(key, String(value));
    }
    const headers = new Headers(options.headers);
    if (op.authenticated && this.apiKey) headers.set('Authorization', 'Bearer ' + this.apiKey);
    return [op, url.toString(), {method: op.method, headers, signal: options.signal, redirect: 'error'}];
  }
  async call<T = unknown>(operation: OperationID, options: Options = {}): Promise<APIResponse<T>> {
    const [op, url, init] = this.request(operation, options);
    if (op.transport) throw new TypeError(`${operation} requires the ${op.transport} API`);
    if (op.requestBody?.required && options.body == null) throw new TypeError('request body is required');
    if (operation === 'executionResolveToolResult') tool(options.body);
    if (options.body !== undefined) { init.body = encodeJSON(options.body); (init.headers as Headers).set('Content-Type', 'application/json'); }
    const safe = op.method === 'GET' || (op.idempotent && Boolean((init.headers as Headers).get('Idempotency-Key')));
    for (let attempt = 0; ; attempt++) {
      let response: Response | undefined;
      try { response = await this.fetch(url, init); }
      catch (error) { if (!safe || attempt >= this.maxRetries || options.signal?.aborted) throw error; }
      if (response && (!safe || !retryStatus.has(response.status) || attempt >= this.maxRetries)) {
        if (!response.ok) throw await apiError(response);
        const text = await response.text(); return {status: response.status, headers: response.headers, data: text ? parseJSON(text) : undefined};
      }
      const ms = delay(response, attempt); await response?.body?.cancel(); await sleep(ms, options.signal);
    }
  }
  async *each<T = unknown>(operation: OperationID, options: Options = {}): AsyncGenerator<T> {
    const page = registry[operation].pagination;
    if (!page) throw new TypeError('operation has no pagination');
    const query = {...options.query}; query[page.request] ??= new Headers(options.headers).get('Last-Event-ID') ?? '0';
    while (true) {
      const response = await this.call<JSONRecord>(operation, {...options, query});
      const items = response.data.data as T[];
      for (const item of items) yield item;
      if (!items.length) return;
      const next = page.itemCursor ? (items[items.length - 1] as JSONRecord)[page.itemCursor] : response.data[page.next!];
      if (next == null) return;
      if (!Number.isSafeInteger(next) || BigInt(next) <= BigInt(String(query[page.request]))) throw new Error('pagination cursor did not advance');
      query[page.request] = String(next);
    }
  }
  async *events(session: string, options: StreamOptions = {}): AsyncGenerator<Event> {
    if (!Number.isInteger(options.maxReconnects ?? 0) || (options.maxReconnects ?? 0) < 0) throw new TypeError('maxReconnects must be a non-negative integer');
    if (options.after !== undefined && options.lastEventID !== undefined) throw new TypeError('provide after or lastEventID, not both');
    let cursor = options.lastEventID;
    for (let attempt = 0; ; attempt++) {
      const headers = new Headers({Accept: 'text/event-stream'});
      const query: Record<string, string> = {};
      if (cursor !== undefined) headers.set('Last-Event-ID', cursor);
      else if (options.after !== undefined) query.after = options.after;
      const [, url, init] = this.request('executionStreamEvents', {path: {id: session}, query, headers, signal: options.signal});
      try {
        const response = await this.fetch(url, init);
        if (response.status !== 200) throw await apiError(response);
        if (response.headers.get('content-type')?.split(';')[0].trim() !== 'text/event-stream' || !response.body) {
          await response.body?.cancel(); throw new Error('expected text/event-stream');
        }
        const parser = new SSEParser();
        for await (const line of lines(response.body)) {
          const event = parser.feed(line);
          if (event) {
            if (!options.taskID || parseJSON(event.data).task_id === options.taskID) yield event;
            cursor = event.id;
          }
        }
      } catch (error) {
        if (options.signal?.aborted || attempt >= (options.maxReconnects ?? 0) ||
          (error instanceof APIError && !retryStatus.has(error.status)) ||
          (!(error instanceof TypeError) && !(error instanceof APIError))) throw error;
      }
      if (attempt >= (options.maxReconnects ?? 0)) return;
      await sleep(delay(undefined, attempt), options.signal);
    }
  }
  async wait(taskID: string, {signal, timeout = 300000, interval = 1000}: {signal?: AbortSignal; timeout?: number; interval?: number} = {}): Promise<WaitResult> {
    const stop = signal ? AbortSignal.any([signal, AbortSignal.timeout(timeout)]) : AbortSignal.timeout(timeout);
    while (true) {
      const task = (await this.call<JSONRecord>('executionGetTask', {path: {id: taskID}, signal: stop})).data;
      if (terminal.has(task.state)) return {task, required_actions: [], reason: 'terminal'};
      const actions = (await this.call<{data: JSONRecord[]}>('executionRequiredActions', {path: {id: task.session_id}, signal: stop})).data.data.filter(a => a.task_id === taskID);
      if (actions.length || task.state === 'unknown') return {task, required_actions: actions, reason: 'action'};
      await sleep(interval, stop);
    }
  }
  approve(task: string, call: string, approve: boolean, signal?: AbortSignal) {
    return this.call('executionResolveToolResult', {path: {id: task, call}, body: {approve}, signal});
  }
  submitResult(task: string, call: string, result: string, {isError = false, errorCode = '', signal}: {isError?: boolean; errorCode?: string; signal?: AbortSignal} = {}) {
    return this.call('executionResolveToolResult', {path: {id: task, call}, body: {result, is_error: isError, error_code: errorCode}, signal});
  }
  async download(operation: OperationID, id: string, write: (chunk: Uint8Array) => void | Promise<void>, options: Pick<Options, 'headers' | 'signal'> = {}): Promise<APIResponse<{bytes: number}>> {
    const [op, url, init] = this.request(operation, {...options, path: {id}});
    if (op.transport !== 'download') throw new TypeError('not a download operation');
    const response = await this.fetch(url, init);
    if (response.status === 304) return {status: 304, headers: response.headers, data: {bytes: 0}};
    if (![200, 206].includes(response.status)) throw await apiError(response);
    if (response.headers.get('content-type')?.startsWith('application/json') || !response.body) {
      await response.body?.cancel(); throw new Error('expected binary download');
    }
    let bytes = 0; const reader = response.body.getReader();
    try { while (true) { const {value, done} = await reader.read(); if (done) break; await write(value); bytes += value.length; } }
    finally { await reader.cancel(); reader.releaseLock(); }
    return {status: response.status, headers: response.headers, data: {bytes}};
  }
  async upload(operation: OperationID, filename: string, source: Blob | AsyncIterable<Uint8Array>, fields: Record<string, string> = {}, signal?: AbortSignal): Promise<APIResponse> {
    const [op, url, init] = this.request(operation, {signal});
    if (op.transport !== 'upload') throw new TypeError('not an upload operation');
    if (source instanceof Blob) {
      const form = new FormData(); for (const [key, value] of Object.entries(fields)) form.append(key, value);
      form.append('file', source, filename); init.body = form;
    } else {
      const boundary = 'wave-' + crypto.randomUUID(); const encoder = new TextEncoder();
      const escape = (value: string) => value.replace(/[\r\n"\\]/g, '_');
      async function* multipart() {
        for (const [key, value] of Object.entries(fields)) yield encoder.encode(`--${boundary}\r\nContent-Disposition: form-data; name="${escape(key)}"\r\n\r\n${value}\r\n`);
        yield encoder.encode(`--${boundary}\r\nContent-Disposition: form-data; name="file"; filename="${escape(filename)}"\r\nContent-Type: application/octet-stream\r\n\r\n`);
        for await (const chunk of source as AsyncIterable<Uint8Array>) yield chunk;
        yield encoder.encode(`\r\n--${boundary}--\r\n`);
      }
      const iterator = multipart();
      init.body = new ReadableStream<Uint8Array>({async pull(controller) { try { const {value, done} = await iterator.next(); if (done) controller.close(); else controller.enqueue(value); } catch (e) { controller.error(e); } }, async cancel() { await iterator.return(undefined); }});
      (init as RequestInit & {duplex: string}).duplex = 'half';
      (init.headers as Headers).set('Content-Type', `multipart/form-data; boundary=${boundary}`);
    }
    const response = await this.fetch(url, init); if (!response.ok) throw await apiError(response);
    return {status: response.status, headers: response.headers, data: parseJSON(await response.text())};
  }
}
