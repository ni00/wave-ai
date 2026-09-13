# Wave AI Python client

[简体中文](README.zh-CN.md) | English

Python 3.10+. Install from this checkout with `pip install ./sdks/python`.

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

`AsyncClient` has corresponding async methods and async iterators. Consume events
with `events(session_id, last_event_id='42')`; close the iterator on early exit
(`contextlib.closing` / `aclosing`). `max_reconnects` defaults to zero; set a bounded
count when desired. Use `approve(task, call, False)` and `submit_result(task, call, '')`
to preserve explicit false and empty results. `wait` returns on terminal state or
required action, without automatically approving or reconciling anything.

`download('filesContent', id, destination)` writes incrementally and leaves the
destination untouched on 304. `upload('filesUpload', filename, file_object)` streams
multipart data. Caller owns file handles and any injected HTTP client.

Every typed model and endpoint is also available in `wave_ai_generated`:

```python
from wave_ai_generated import AgentsApi
with Client(api_key=os.environ['WAVE_API_KEY']).raw() as raw:
    response = AgentsApi(raw).agents_list()
```

Raw generated endpoints do not include the public client's streaming, validation,
pagination or retry behavior. Public calls retry reads and explicitly keyed
idempotent writes only. See the [CLI guide](../../cli/README.md) and [generation and release guide](../../tools/clients/README.md).

API documentation is available in [Chinese](../../api/openapi.zh-CN.json) and [English](../../api/openapi.json), with identical contract structure.
