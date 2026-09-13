import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {Client, generated} from '../../sdks/typescript/dist/index.js';

const f=JSON.parse(readFileSync(new URL('./workflow.json',import.meta.url),'utf8'));
const c=new Client({baseURL:process.env.WAVE_BASE_URL,apiKey:process.env.WAVE_API_KEY});
const signal=AbortSignal.timeout(30000);
const agent=(await c.call('agentsCreate',{body:f.agent,signal})).data;
const session=(await c.call('executionCreateSession',{body:f.session,signal})).data;
const task=(await c.call('executionCreateTask',{path:{id:session.id},body:{agent_id:agent.id,input:f.input},signal})).data;
let action=await c.wait(task.id,{signal,interval:10});
assert.equal(action.required_actions[0].type,'approve_tool');
const call=action.required_actions[0].call_id;
await c.approve(task.id,call,true,signal);
action=await c.wait(task.id,{signal,interval:10});
assert.equal(action.required_actions[0].type,'submit_tool_result');
await c.submitResult(task.id,call,'',{signal});
assert.equal((await c.wait(task.id,{signal,interval:10})).task.state,'succeeded');
assert.equal((await new generated.ExecutionApi(c.rawConfiguration()).executionGetTask({id:task.id})).state,'succeeded');
for await(const event of c.events(session.id,{lastEventID:'0',taskID:task.id,signal})){assert.ok(event.id);break;}
async function* source(){yield new TextEncoder().encode(f.file);}
const file=(await c.upload('filesUpload','中文.txt',source(),{},signal)).data;
const chunks=[];await c.download('filesContent',file.id,b=>chunks.push(b),{signal});
assert.equal(Buffer.concat(chunks).toString(),f.file);
console.log('TypeScript real-service workflow passed');
