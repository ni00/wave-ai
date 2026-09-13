# Wave AI Go 客户端

中文 | [English](README.md)

模块：`github.com/ni00/wave-ai/sdks/go`，要求 Go 1.23+。首个公开模块标签发布前，使用本地 `replace` 指向该目录。

```go
client, err := wave.New("http://localhost:8080", os.Getenv("WAVE_API_KEY"))
if err != nil { return err }
ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
defer cancel()
agents, _, err := client.Raw().AgentsAPI.AgentsList(ctx).Execute()
```

以 `wave` 导入该包，生成的类型位于 `/generated` 子包。`Raw` 提供完整的类型化 API；`Call` 通过稳定的 operationId 和 `Options{Path, Query, Headers, Body}` 调用 JSON 操作。原始端点遵循生成器语义，公共辅助方法额外提供校验、有限的安全重试和工作流。

- `Each(ctx, operation, options, visit)` 遍历分页条目。
- `Events(ctx, session, StreamOptions{LastEventID:"42"}, visit)` 流式接收完整事件；取消 ctx 或返回错误即可停止。重连需显式启用且次数有限。
- `Wait(ctx, taskID, interval)` 只跟踪指定任务，返回 `terminal` 或 `action`。
- `Approve(..., false)` 和 `SubmitResult(..., "", false, "")` 保留显式值。
- `Download(ctx, "filesContent", id, headers, writer)` 流式下载，304 不写入；206 时先检查 Content-Range，再决定如何保存范围内容。
- `Upload(ctx, "filesUpload", filename, reader, fields)` 流式发送 multipart 数据。

调用方负责 reader/writer 的生命周期，并通过 context 控制截止时间。文件传输不会自动重放。`APIError` 包含状态码、错误类型和 request ID。`Operations()` 和 `Specification()` 暴露生成的命令映射与契约。

接口说明提供[中文](../../api/openapi.zh-CN.json)和[英文](../../api/openapi.json)，两版协议结构相同。另见 [CLI 指南](../../cli/README.zh-CN.md)及[生成与发布说明](../../tools/clients/README.zh-CN.md)。
