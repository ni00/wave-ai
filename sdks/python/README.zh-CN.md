# Wave AI Python 客户端

中文 | [English](README.md)

要求 Python 3.10+。从仓库安装：`pip install ./sdks/python`。

```python
import os
from wave_ai import Client

with Client(api_key=os.environ['WAVE_API_KEY']) as wave:
    agents = list(wave.each('agentsList'))
    session = wave.call('executionCreateSession', body={'title': 'Demo'}).data
    task = wave.call('executionCreateTask', path={'id': session['id']},
                     body={'agent_id': agents[0]['id'], 'input': 'Hello'}).data
    result = wave.wait(task['id'])
    print(result.reason, result.task)
```

`AsyncClient` 提供对应的异步方法和异步迭代器。通过 `events(session_id, last_event_id='42')` 消费事件，提前退出时用 `contextlib.closing` / `aclosing` 关闭迭代器。`max_reconnects` 默认为零，需要重连时设置有限次数。`approve(task, call, False)` 和 `submit_result(task, call, '')` 保留显式 false 和空结果。`wait` 遇到终态或待处理动作即返回，不自动审批或 reconcile。

`download('filesContent', id, destination)` 增量写入，304 不修改目标。`upload('filesUpload', filename, file_object)` 流式发送 multipart 数据。调用方负责文件句柄和注入的 HTTP 客户端的生命周期。

完整的类型化模型和端点也可通过 `wave_ai_generated` 使用：

```python
from wave_ai_generated import AgentsApi
with Client(api_key=os.environ['WAVE_API_KEY']).raw() as raw:
    response = AgentsApi(raw).agents_list()
```

原始生成端点不包含公共客户端的流式处理、校验、分页和重试行为。公共调用只重试读取和显式携带幂等键的幂等写入。另见 [CLI 指南](../../cli/README.zh-CN.md)及[生成与发布说明](../../tools/clients/README.zh-CN.md)。

接口说明提供[中文](../../api/openapi.zh-CN.json)和[英文](../../api/openapi.json)，两版协议结构相同。
