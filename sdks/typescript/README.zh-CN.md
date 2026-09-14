# Wave AI TypeScript SDK

[English](README.md) | 简体中文

Wave AI 的 TypeScript 客户端，包名为 `@wave-ai/client`，使用 ESM，附带类型声明。

支持 Node.js 22+，以及提供 Fetch、Streams、`AbortSignal.timeout` 和 `AbortSignal.any` 的浏览器。浏览器直连需要配置 CORS；Wave API 密钥应保存在可信后端。

## 1. 安装

从本地仓库构建，再安装到应用中：

```bash
# 在 Wave 仓库根目录执行。
npm ci --prefix sdks/typescript
npm run build --prefix sdks/typescript

# 在应用目录执行，替换为 Wave 仓库的实际路径。
npm install /path/to/wave-ai/sdks/typescript
```

## 2. 创建并等待任务

先[启动 Wave](../../README.zh-CN.md#2-快速开始)，设置 `WAVE_API_KEY`，并将 `YOUR_MODEL` 替换为供应商支持的模型。以下示例在 Node.js 中运行。

```typescript
import { Client } from '@wave-ai/client';

type Created = { id: string };
const wave = new Client({ apiKey: process.env.WAVE_API_KEY });
const agent = await wave.call<Created>('agentsCreate', {
  body: { name: 'assistant', model: 'YOUR_MODEL' },
});
const session = await wave.call<Created>('executionCreateSession', {
  body: { title: 'Demo' },
});
const task = await wave.call<Created>('executionCreateTask', {
  path: { id: session.data.id },
  headers: { 'Idempotency-Key': `first-run-${session.data.id}` },
  body: { agent_id: agent.data.id, input: 'Say hello' },
});
const result = await wave.wait(task.data.id);
console.log(result.reason, result.task.state);
```

构造参数包括 `baseURL`（默认 `http://localhost:8080`）、`apiKey`、`fetch` 和 `maxRetries`（默认 2）。可注入 `fetch` 定制传输。

## 3. API 参考

`call<T>` 按 OpenAPI 的 `operationId` 调用 JSON 接口，返回含 `status`、`headers` 和 `data` 的 `APIResponse<T>`。泛型 `T` 用于静态类型检查，不校验响应数据。

| 方法 | 作用 |
| --- | --- |
| `call<T>` | 调用单个 JSON 操作，返回 `APIResponse<T>` |
| `each<T>` | 遍历分页操作并逐项产出 |
| `events` | 以异步生成器迭代会话事件流 |
| `wait` | 轮询直到任务结束或需要处理动作 |
| `approve(task, call, approve, signal?)` | 允许或拒绝某个工具调用 |
| `submitResult(task, call, result, {isError, errorCode, signal})` | 提交客户端工具调用的实际结果 |
| `download` | 通过回调流式接收文件字节 |
| `upload` | 从 `Blob` 或异步可迭代对象流式上传 |

需要带类型的模型和端点时，使用 `generated` 导出：

```typescript
import { generated } from '@wave-ai/client';

const api = new generated.AgentsApi(wave.rawConfiguration());
const agents = await api.agentsList();
```

生成 API 不提供封装层的请求校验、自动分页、重试和安全整数检查。

## 4. 处理待办动作

`wait` 返回后，`reason` 为 `terminal` 时检查 `task.state` 是否成功；为 `action` 时按 `required_actions` 处理：

| 动作 | 处理方式 |
| --- | --- |
| `approve_tool` | 确认允许执行后调用 `approve`；传 `false` 拒绝 |
| `submit_tool_result` | 执行客户端工具后，通过 `submitResult` 提交实际结果 |
| `confirm_tool_outcome`、`reconcile_task` | 核实外部执行情况后，按[恢复流程](../../skills/wave-client/references/recovery.md)处理 |

客户端工具获批后仍需提交执行结果。处理动作后，再调用 `wait`。

## 5. 读取事件

```typescript
const signal = AbortSignal.timeout(60_000);
for await (const event of wave.events(sessionID, { maxReconnects: 3, signal })) {
  console.log(event.id, event.type, event.data);
}
```

传入 `AbortSignal` 可终止事件流；提前 `break` 也会关闭响应。默认不重连，设置 `maxReconnects` 后按指定次数重试。

续读时传入该会话保存的 `lastEventID`，不能同时指定 `after`。重连后按事件 ID 去重。

## 6. 传输文件

```typescript
const response = await wave.download('filesContent', fileID, writeChunk);
await wave.upload('filesUpload', 'report.txt', source);
```

`writeChunk` 每次接收一个 `Uint8Array`；下载会等待回调完成。`source` 可为 `Blob` 或异步可迭代的字节流。

- HTTP 304：不写入目标。
- HTTP 206：返回部分内容，按响应头中的 `Content-Range` 决定如何保存。
- 传输中断可能留下部分数据；上传和下载均不自动重试。

## 7. 错误、重试与超时

HTTP API 错误抛出 `APIError`，包含 `status`、`code`、`message` 和 `requestID`。参数错误、未知操作或缺少请求体抛出 `TypeError`；JSON 整数超出安全范围时抛出 `RangeError`，避免 int64 值被静默舍入。

自动重试仅适用于 `GET`，或契约标记为幂等且携带 `Idempotency-Key` 的写请求。对网络错误及 HTTP 429、502、503、504，默认最多重试 2 次。

普通请求通过 `AbortSignal` 控制超时。`wait` 默认等待 300,000 毫秒，每 1,000 毫秒轮询；`timeout` 和 `interval` 均以毫秒为单位，也可传入 `signal` 提前终止。客户端超时不会取消远端任务；继续查询已有任务 ID，重试同一次提交时复用幂等键。

## 8. 相关文档

[中文 API 契约](../../api/openapi.zh-CN.json) · [英文 API 契约](../../api/openapi.json) · [CLI 指南](../../cli/README.zh-CN.md) · [生成与发布](../../tools/clients/README.zh-CN.md)
