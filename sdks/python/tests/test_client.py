import asyncio
from contextlib import closing, aclosing
from io import BytesIO
import json
import os
from pathlib import Path
import time

import pytest
from wave_ai import Client, AsyncClient, APIError
from wave_ai_generated import ModelclientUsage, AgentsApi

FIXTURE = json.loads((Path(__file__).resolve().parents[3] / 'tests/clients/fixtures.json').read_text())

def client(scenario, asynchronous=False):
    base = os.environ.get('WAVE_CLIENT_TEST_URL')
    if not base:
        pytest.skip('run make clients-test')
    return (AsyncClient if asynchronous else Client)(f'{base}/python-{asynchronous}/{scenario}', 'Bearer '+FIXTURE['key'])

def test_serialization():
    assert ModelclientUsage.from_dict(FIXTURE['usage']).to_dict() == FIXTURE['usage']

def test_sync():
    with client('error') as c:
        with pytest.raises(APIError) as error: c.call('agentsList')
        assert error.value.code == 'permission_error'
        assert error.value.request_id == 'req_contract'
        with c.raw() as raw:
            with pytest.raises(Exception) as raw_error: AgentsApi(raw).agents_list()
            assert raw_error.value.status == 403
    with client('approve') as c: c.approve('task','call',False)
    with client('empty') as c: c.submit_result('task','call','')
    for scenario, op in [('offset','agentsList'),('after','executionMessages'),('sequence','executionListEvents')]:
        with client(scenario) as c: assert len(list(c.each(op, path={'id':'session'}))) == 1
    with client('stuck') as c:
        with pytest.raises(ValueError): list(c.each('agentsList'))
    for scenario in ['retry','no-retry']:
        with client(scenario) as c:
            kwargs = dict(path={'id':'session'}, body={'agent_id':'agent_test','input':'hello'})
            if scenario == 'retry':
                assert c.call('executionCreateTask',headers={'Idempotency-Key':'stable-key'},**kwargs).status == 202
            else:
                with pytest.raises(APIError): c.call('executionCreateTask',**kwargs)
    for scenario in ['sse','reconnect']:
        with client(scenario) as c:
            start = time.monotonic()
            with closing(c.events('session',last_event_id='42',task_id='task_target',max_reconnects=1)) as events:
                first = next(events)
                assert first.id == '44' and first.type == 'custom.future'
                assert time.monotonic()-start < 1
                if scenario == 'reconnect': assert next(events).id == '45'
    with client('wait-child') as c: assert c.wait('task_target', interval=.001).reason == 'terminal'
    with client('wait-action') as c: assert c.wait('task_target').reason == 'action'
    for scenario in ['download','range','not-modified','range-error','json-file']:
        with client(scenario) as c:
            out = BytesIO()
            if scenario in ['range-error','json-file']:
                with pytest.raises((APIError,ValueError)): c.download('filesContent','file',out)
                assert out.getvalue() == b''
            else:
                c.download('filesContent','file',out,headers={'Range':'bytes=0-3'})
                assert out.getvalue() == (b'' if scenario=='not-modified' else b'Wave' if scenario=='range' else FIXTURE['binary'].encode())
    with client('upload') as c:
        assert c.upload('filesUpload','中文.txt',BytesIO(FIXTURE['binary'].encode())).status == 201

def test_async():
    async def run():
        async with client('error',True) as c:
            with pytest.raises(APIError) as error: await c.call('agentsList')
            assert error.value.request_id == 'req_contract'
        async with client('approve',True) as c: await c.approve('task','call',False)
        async with client('empty',True) as c: await c.submit_result('task','call','')
        for scenario, op in [('offset','agentsList'),('after','executionMessages'),('sequence','executionListEvents')]:
            async with client(scenario,True) as c: assert len([x async for x in c.each(op,path={'id':'session'})]) == 1
        for scenario in ['sse','reconnect']:
            async with client(scenario,True) as c:
                async with aclosing(c.events('session',last_event_id='42',task_id='task_target',max_reconnects=1)) as events:
                    first = await asyncio.wait_for(anext(events),1)
                    assert first.id == '44'
                    if scenario=='reconnect': assert (await anext(events)).id=='45'
        async with client('wait-child',True) as c: assert (await c.wait('task_target',interval=.001)).reason == 'terminal'
        async with client('wait-action',True) as c: assert (await c.wait('task_target')).reason == 'action'
        async with client('wait-timeout',True) as c:
            with pytest.raises(TimeoutError): await c.wait('task_target',timeout=.03)
        async with client('download',True) as c:
            out=BytesIO();await c.download('filesContent','file',out);assert out.getvalue()==FIXTURE['binary'].encode()
        async with client('upload',True) as c:
            assert (await c.upload('filesUpload','中文.txt',BytesIO(FIXTURE['binary'].encode()))).status==201
    asyncio.run(run())
