from contextlib import closing
from io import BytesIO
import json
import os
from pathlib import Path
import subprocess
import tempfile

from wave_ai import Client

fixture=json.loads(Path(__file__).with_name('workflow.json').read_text())
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

def cli(*args, input=None, expected=0):
    process=subprocess.run([os.environ['WAVE_TEST_CLI'],*args],input=input,text=True,capture_output=True)
    assert process.returncode==expected,(args,process.returncode,process.stderr)
    return json.loads(process.stdout)

# Execute the same commands documented in the external agent skill.
tool_asset=Path(__file__).resolve().parents[2]/'skills/wave-client/assets/client-tool.json'
agent=cli('agents','create','--name',fixture['agent']['name'],'--model',fixture['agent']['model'],'--tools','@'+str(tool_asset))
session=cli('sessions','create','--title',fixture['session']['title'])
action=cli('tasks','create','--session',session['id'],'--agent',agent['id'],'--input-file','-',
           '--idempotency-key','cli-skill-workflow','--wait',input=fixture['input'],expected=6)
task=action['task']
assert cli('tasks','get',task['id'],'--select','/id')==task['id']
call=action['required_actions'][0]['call_id']
cli('tools','approve',call,'--task',task['id'])
action=cli('tasks','wait',task['id'],'--interval','10ms',expected=6)
assert action['required_actions'][0]['type']=='submit_tool_result'
cli('tools','result',call,'--task',task['id'],'--result-file','-',input='')
assert cli('tasks','wait',task['id'],'--interval','10ms')['task']['state']=='succeeded'
with tempfile.TemporaryDirectory() as directory:
    uploaded=cli('files','upload','--file','-','--filename','中文.txt',input=fixture['file'])
    target=Path(directory)/'downloaded.txt';cli('files','download',uploaded['id'],'--output',str(target))
    assert target.read_text()==fixture['file']
print('Python and CLI real-service workflows passed')
