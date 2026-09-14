# Wave AI Go SDK

[English](README.md) | 简体中文

Wave AI 的 Go 客户端。模块路径为 `github.com/ni00/wave-ai/sdks/go`，包名为 `wave`。需要 Go 1.23+。

## 1. 安装

在应用的 `go.mod` 中引用本地 SDK，将路径替换为仓库的实际位置：

```go
require github.com/ni00/wave-ai/sdks/go v0.0.0

replace github.com/ni00/wave-ai/sdks/go => /path/to/wave-ai/sdks/go
```

## 2. 创建并等待任务

先[启动 Wave](../../README.zh-CN.md#2-快速开始)，设置 `WAVE_API_KEY`，并将 `YOUR_MODEL` 替换为供应商支持的模型。

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	wave "github.com/ni00/wave-ai/sdks/go"
)

func main() {
	client, err := wave.New("http://localhost:8080", os.Getenv("WAVE_API_KEY"))
	if err != nil {
		panic(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	agent := create(ctx, client, "agentsCreate", nil,
		map[string]any{"name": "assistant", "model": "YOUR_MODEL"})
	session := create(ctx, client, "executionCreateSession", nil,
		map[string]any{"title": "demo"})
	task := create(ctx, client, "executionCreateTask", map[string]string{"id": session},
		map[string]any{"agent_id": agent, "input": "Say hello"})

	result, err := client.Wait(ctx, task, 2*time.Second)
	if err != nil {
		panic(err)
	}
	fmt.Println(result.Reason, result.Task["state"])
}

func create(ctx context.Context, c *wave.Client, op string, path map[string]string, body map[string]any) string {
	res, err := c.Call(ctx, op, wave.Options{Path: path, Body: body})
	if err != nil {
		panic(err)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(res.Data, &out); err != nil {
		panic(err)
	}
	return out.ID
}
```

`New` 默认连接 `http://localhost:8080`，地址须为不含凭据、查询参数和片段的 HTTP(S) URL。可通过 `HTTPClient` 设置代理或自定义传输，通过 `MaxRetries` 调整重试次数。

## 3. 调用 API

`Call` 通过 OpenAPI 的 `operationId` 调用 JSON 接口；`Options` 接收路径、查询参数、请求头和请求体。上传、下载和事件流使用专用方法。

需要带类型的模型和端点时，使用生成客户端：

```go
agents, response, err := client.Raw().AgentsAPI.AgentsList(ctx).Execute()
```

生成客户端不提供封装层的请求校验、自动分页和重试。

### 3.1 方法参考

| 方法 | 作用 |
| --- | --- |
| `Call` | 调用单个 JSON 操作，返回 `*Response` |
| `Each` | 遍历分页操作，每项一个 `json.RawMessage` |
| `Wait` | 轮询直到任务进入终态或需要处理动作 |
| `Approve(ctx, task, call, approve)` | 允许或拒绝某个工具调用 |
| `SubmitResult(ctx, task, call, result, isError, errorCode)` | 提交客户端工具调用的实际结果 |
| `Events` | 消费会话事件流 |
| `Download` | 把文件字节流写入 `io.Writer` |
| `Upload` | 从 `io.Reader` 流式发送 multipart 上传 |

## 4. 处理待办动作

`Wait` 返回后，`Reason` 为 `terminal` 时检查 `Task["state"]` 是否成功；为 `action` 时按 `RequiredActions` 处理：

| 动作 | 处理方式 |
| --- | --- |
| `approve_tool` | 确认允许执行后调用 `Approve`；传 `false` 拒绝 |
| `submit_tool_result` | 执行客户端工具后，通过 `SubmitResult` 提交实际结果 |
| `confirm_tool_outcome`、`reconcile_task` | 核实外部执行情况后，按[恢复流程](../../skills/wave-client/references/recovery.md)处理 |

客户端工具获批后仍需提交执行结果。处理动作后，再调用 `Wait`。

## 5. 读取事件

```go
err = client.Events(ctx, sessionID,
	wave.StreamOptions{LastEventID: savedCursor, MaxReconnects: 3},
	func(event wave.Event) error {
		fmt.Println(event.ID, event.Type, event.Data)
		return nil
	})
```

`savedCursor` 是该会话上次保存的事件 ID；首次读取可用空字符串。默认不重连，设置 `MaxReconnects` 后按指定次数重试。取消 `ctx` 或让回调返回错误可停止读取。

`After` 与 `LastEventID` 不能同时使用。重连后按事件 ID 去重。

## 6. 传输文件

`Download` 将响应流写入 `io.Writer`，`Upload` 从 `io.Reader` 流式上传 multipart 数据。调用方负责关闭文件和保存结果。

- HTTP 304：不写入目标。
- HTTP 206：返回部分内容，按 `Content-Range` 决定如何保存。
- 传输中断可能留下部分数据；上传和下载均不自动重试。

## 7. 错误、重试与超时

HTTP API 错误返回 `*wave.APIError`，包含 `StatusCode`、`Code`、`Message` 和 `RequestID`。本地校验、传输和 context 错误保留各自类型。

自动重试仅适用于 `GET`，或契约标记为幂等且携带 `Idempotency-Key` 的写请求。对网络错误及 HTTP 429、502、503、504，默认最多重试 2 次。

客户端和默认的 `http.Client` 均不设置总超时。通过 `context` 控制请求、重试和等待的截止时间；客户端超时不会取消远端任务。

## 8. 相关文档

[中文 API 契约](../../api/openapi.zh-CN.json) · [英文 API 契约](../../api/openapi.json) · [CLI 指南](../../cli/README.zh-CN.md) · [生成与发布](../../tools/clients/README.zh-CN.md)
