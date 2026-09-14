# Wave AI Python SDK

[English](README.md) | 简体中文

Wave AI 的 Python 客户端，支持同步和异步调用。需要 Python 3.10+；安装包名为 `wave-ai-client`，导入包名为 `wave_ai`。

## 安装

在仓库根目录执行：

```bash
python -m pip install ./sdks/python
```

## 创建并等待任务

先[启动 Wave](../../README.zh-CN.md#快速开始)，设置 `WAVE_API_KEY`，并将 `YOUR_MODEL` 替换为供应商支持的模型。

```python
import os

from wave_ai import Client

with Client(api_key=os.environ["WAVE_API_KEY"]) as wave:
    agent = wave.call(
        "agentsCreate", body={"name": "assistant", "model": "YOUR_MODEL"}
    ).data
    session = wave.call("executionCreateSession", body={"title": "Demo"}).data
    task = wave.call(
        "executionCreateTask",
        path={"id": session["id"]},
        headers={"Idempotency-Key": f"first-run-{session['id']}"},
        body={"agent_id": agent["id"], "input": "Say hello"},
    ).data
    result = wave.wait(task["id"])
    print(result.reason, result.task["state"])
```

默认地址为 `http://localhost:8080`，通过 `base_url` 修改。可设置 `timeout`、`max_retries`，或注入自定义 `http_client`。上下文管理器只关闭 SDK 创建的连接，注入的客户端由调用方关闭。

## API 参考

`call` 按 OpenAPI 的 `operationId` 调用 JSON 接口；事件流和文件传输使用专用方法。后续片段中的 `wave` 均为已打开的客户端。

| 方法 | 作用 |
| --- | --- |
| `call` | 调用单个 JSON 操作 |
| `each` | 遍历分页操作并逐项产出 |
| `events` | 迭代会话事件流 |
| `wait` | 轮询直到任务结束或需要处理动作 |
| `approve(task, call, approve)` | 允许或拒绝某个工具调用 |
| `submit_result(task, call, result, *, is_error, error_code)` | 提交客户端工具调用的实际结果 |
| `download` | 把文件字节流写入二进制文件对象 |
| `upload` | 从文件对象流式发送 multipart 上传 |

需要类型化端点时，使用 `wave_ai_generated`：

```python
from wave_ai_generated import AgentsApi

with Client(api_key=os.environ["WAVE_API_KEY"]) as wave:
    with wave.raw() as raw:
        agents = AgentsApi(raw).agents_list()
```

生成 API 不提供封装层的请求校验、自动分页和重试，错误类型为 `OpenApiException` 的子类。

## 处理待办动作

`wait` 返回后，`reason` 为 `terminal` 时检查 `task["state"]` 是否成功；为 `action` 时按 `required_actions` 处理：

| 动作 | 处理方式 |
| --- | --- |
| `approve_tool` | 确认允许执行后调用 `approve`；传 `False` 拒绝 |
| `submit_tool_result` | 执行客户端工具后，通过 `submit_result` 提交实际结果 |
| `confirm_tool_outcome`、`reconcile_task` | 核实外部执行情况后，按[恢复流程](../../skills/wave-client/references/recovery.md)处理 |

客户端工具获批后仍需提交执行结果。处理动作后，再调用 `wait`。

## 读取事件与传输文件

```python
from contextlib import closing

with closing(wave.events(session_id, max_reconnects=3)) as events:
    for event in events:
        print(event.id, event.type, event.data)
```

默认不重连。设置 `max_reconnects` 可限制重连次数；续读时传入该会话保存的 `last_event_id`，不能同时指定 `after`。重连后按事件 ID 去重，提前退出时关闭迭代器。

`download` 增量写入二进制文件对象，`upload` 从文件对象上传。调用方负责关闭文件，并按 HTTP 206 响应的 `Content-Range` 保存部分内容。HTTP 304 不写入目标；传输中断可能留下部分数据。两种传输均不自动重试。

## 使用异步客户端

`AsyncClient` 提供对应的异步方法，分页与事件流通过异步生成器读取：

```python
from contextlib import aclosing

from wave_ai import AsyncClient

async def read_events(session_id: str):
    async with AsyncClient(api_key=os.environ["WAVE_API_KEY"]) as wave:
        async with aclosing(wave.events(session_id)) as events:
            async for event in events:
                print(event.id, event.type, event.data)
```

## 错误、重试与超时

HTTP API 错误抛出 `APIError`，包含 `status`、`code`、`message` 和 `request_id`。本地校验及传输错误保留各自类型。

网络错误和 HTTP 429、502、503、504 默认最多重试两次，仅限 `GET` 和契约标记为幂等、带 `Idempotency-Key` 的写请求。

| 配置 | 含义 |
| --- | --- |
| 构造参数 `timeout` | 默认 30 秒，传给 HTTPX 的请求超时配置；不是包含重试的总时限 |
| `Client.call(timeout=...)` | 跨重试共享的时间预算，单位为秒 |
| `AsyncClient.call(timeout=...)` | 每次尝试的请求超时；重试会延长总耗时 |
| `wait(timeout=300.0, interval=1.0)` | 等待预算与轮询间隔，单位为秒 |

`wait` 超过等待预算会抛出 `TimeoutError`，底层请求也可能先抛出 HTTPX 超时异常。客户端超时不会取消远端任务；继续查询已有任务 ID，重试同一次提交时复用幂等键。

## 相关文档

[中文 API 契约](../../api/openapi.zh-CN.json) · [英文 API 契约](../../api/openapi.json) · [CLI 指南](../../cli/README.zh-CN.md) · [生成与发布](../../tools/clients/README.zh-CN.md)
