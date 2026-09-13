from contextlib import closing
from io import BytesIO
import json
import os
from pathlib import Path

from wave_ai import Client

fixture=json.loads((Path(__file__).resolve().parents[3]/'tests/clients/workflow.json').read_text())
with Client(os.environ['WAVE_BASE_URL'],os.environ['WAVE_API_KEY']) as wave:
    agent=wave.call('agentsCreate',body=fixture['agent']).data
    session=wave.call('executionCreateSession',body=fixture['session']).data
    task=wave.call('executionCreateTask',path={'id':session['id']},body={'agent_id':agent['id'],'input':fixture['input']}).data
    action=wave.wait(task['id'],interval=.01)
    assert action.reason=='action' and action.required_actions[0]['type']=='approve_tool'
    call=action.required_actions[0]['call_id']
    wave.approve(task['id'],call,True)
    assert wave.wait(task['id'],interval=.01).required_actions[0]['type']=='submit_tool_result'
    wave.submit_result(task['id'],call,'')
    assert wave.wait(task['id'],interval=.01).task['state']=='succeeded'
    with closing(wave.events(session['id'],last_event_id='0',task_id=task['id'])) as events:
        assert next(events).id
    with wave.raw() as raw:
        from wave_ai_generated import ExecutionApi
        assert ExecutionApi(raw).execution_get_task(task['id']).state=='succeeded'
    file=wave.upload('filesUpload','中文.txt',BytesIO(fixture['file'].encode())).data
    output=BytesIO();wave.download('filesContent',file['id'],output)
    assert output.getvalue()==fixture['file'].encode()

print('Python SDK real-service workflow passed')
