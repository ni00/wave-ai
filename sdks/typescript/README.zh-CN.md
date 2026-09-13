# Wave AI TypeScript 客户端

中文 | [English](README.md)

支持 Node.js 22+，以及提供 Fetch、流和 AbortSignal.timeout/any 的现代浏览器。使用 `npm ci && npm run build` 构建；包提供原生 ESM 和类型声明。

```typescript
import { Client, generated } from '@wave-ai/client';
const wave = new Client({apiKey: process.env.WAVE_API_KEY});
const api = new generated.AgentsApi(wave.rawConfiguration());
const agents = await api.agentsList();
for await (const event of wave.events('session_id', {lastEventID: '42'})) {
  console.log(event.id, event.data);
}
```

`call(operationId, options)` 覆盖 JSON 端点；生成的 API 提供完整模型类型。`each` 遍历分页，`wait` 在终态或待处理动作时返回，`approve(..., false)` 和 `submitResult(..., '')` 保留显式值。传入 AbortSignal 可取消请求和事件流；退出事件迭代会关闭响应。通过 `maxReconnects` 显式启用有限次数重连。

`download('filesContent', id, writeChunk)` 等待每次写入完成，保留 206 响应元数据，304 时不写入。`upload('filesUpload', name, blobOrAsyncIterable)` 支持浏览器 Blob 和 Node 流式数据源。浏览器跨域请求需要部署适当配置 CORS；服务端密钥应留在可信后端，不应放入公开分发的前端包。

公共客户端会拒绝 JSON 中超出安全整数范围的值，避免静默舍入 int64 计数。类型化生成方法仍使用生成器原生的 number 和传输行为。另见 [CLI 指南](../../cli/README.zh-CN.md)及[生成与发布说明](../../tools/clients/README.zh-CN.md)。

接口说明提供[中文](../../api/openapi.zh-CN.json)和[英文](../../api/openapi.json)，两版协议结构相同。
