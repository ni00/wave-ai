import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {Client, APIError, models, parseJSON} from '../dist/index.js';

const fixtures=JSON.parse(readFileSync(new URL('../../../tests/clients/fixtures.json',import.meta.url),'utf8'));
const base=process.env.WAVE_CLIENT_TEST_URL;
const client=scenario=>new Client({baseURL:`${base}/ts/${scenario}`,apiKey:'Bearer '+fixtures.key});
test('null and unsafe numbers',()=>{
  assert.deepEqual(models.ModelclientUsageToJSON(models.ModelclientUsageFromJSON(fixtures.usage)),fixtures.usage);
  assert.throws(()=>parseJSON('{"sequence":9007199254740993}'),RangeError);
});
test('shared HTTP contracts',{skip:!base},async()=>{
  await assert.rejects(client('error').call('agentsList'),e=>e instanceof APIError&&e.code==='permission_error'&&e.requestID==='req_contract');
  await assert.rejects(client('error').agentsList(),e=>e instanceof APIError&&e.status===403);
  await client('approve').approve('task','call',false);
  await client('empty').submitResult('task','call','');
  for(const [scenario,op] of [['offset','agentsList'],['after','executionMessages'],['sequence','executionListEvents']]){
    const items=[];for await(const item of client(scenario).each(op,{path:{id:'session'}}))items.push(item);assert.equal(items.length,1);
  }
  await assert.rejects(async()=>{for await(const _ of client('stuck').each('agentsList')){}},/did not advance/);
  const options={path:{id:'session'},body:{agent_id:'agent_test',input:'hello'}};
  assert.equal((await client('retry').call('executionCreateTask',{...options,headers:{'Idempotency-Key':'stable-key'}})).status,202);
  await assert.rejects(client('no-retry').call('executionCreateTask',options),APIError);
  await assert.rejects(client('no-retry-empty').call('executionCreateTask',{...options,headers:{'Idempotency-Key':''}}),APIError);
  for(const scenario of ['sse','reconnect']){
    const start=Date.now();const events=client(scenario).events('session',{lastEventID:'42',taskID:'task_target',maxReconnects:1,signal:AbortSignal.timeout(1000)});
    try{const first=(await events.next()).value;assert.equal(first.id,'44');assert.equal(first.type,'custom.future');assert.equal(JSON.parse(first.data).text,'中文');assert.ok(Date.now()-start<1000);
      if(scenario==='reconnect')assert.equal((await events.next()).value.id,'45');
    }finally{await events.return();}
  }
  assert.equal((await client('wait-child').wait('task_target',{interval:1})).reason,'terminal');
  assert.equal((await client('wait-action').wait('task_target')).reason,'action');
  await assert.rejects(client('wait-timeout').wait('task_target',{timeout:30}),e=>e.name==='TimeoutError');
  for(const scenario of ['download','range','not-modified','range-error','json-file']){
    const chunks=[];const request=client(scenario).download('filesContent','file',b=>{chunks.push(b)},{headers:{Range:'bytes=0-3'}});
    if(['range-error','json-file'].includes(scenario)){await assert.rejects(request);assert.equal(chunks.length,0);}
    else{await request;assert.equal(Buffer.concat(chunks).toString(),scenario==='not-modified'?'':scenario==='range'?'Wave':fixtures.binary);}
  }
  assert.equal((await client('upload').upload('filesUpload','中文.txt',new Blob([fixtures.binary]))).status,201);
  async function* data(){yield new TextEncoder().encode(fixtures.binary);}
  assert.equal((await client('upload').upload('filesUpload','中文.txt',data())).status,201);
});

test('typed methods convert snake_case wire format',async()=>{
 let captured;
 const stub=async(url,init)=>{captured={url,body:init?.body};return new Response(JSON.stringify({
   id:'sess_1',title:'t',archived:false,created_at:'2026-09-18T00:00:00Z',
   event_sequence:1,message_sequence:1,file_ids:null,memory_store_ids:['memory_1'],
 }),{status:201,headers:{'content-type':'application/json'}});};
 const sdk=new Client({baseURL:'http://localhost:8080',fetch:stub});
 const session=await sdk.executionCreateSession({title:'t',memoryStoreIds:['memory_1']});
 assert.deepEqual(JSON.parse(captured.body),{title:'t',memory_store_ids:['memory_1']});
 assert.equal(session.memoryStoreIds[0],'memory_1');
 assert.ok(session.createdAt instanceof Date);
});

// wait(): SSE-first with backoff polling fallback, exercised against a scripted fake server.
import {createServer} from 'node:http';

function scripted({states, actions = [], events = [], hold = true}) {
  const counts = {task: 0, actions: 0, stream: 0};
  const handler = (req, res) => {
    const {pathname} = new URL(req.url, 'http://localhost');
    if (pathname === '/v1/tasks/task1') {
      const state = states[Math.min(counts.task, states.length - 1)]; counts.task++;
      res.writeHead(200, {'content-type': 'application/json'});
      res.end(JSON.stringify({id: 'task1', session_id: 'sess1', state}));
    } else if (pathname === '/v1/sessions/sess1/required_actions') {
      counts.actions++;
      res.writeHead(200, {'content-type': 'application/json'});
      res.end(JSON.stringify({data: actions}));
    } else if (pathname === '/v1/sessions/sess1/events/stream') {
      counts.stream++;
      res.writeHead(200, {'content-type': 'text/event-stream'});
      res.write(events.map(e => `id: ${e.id}\nevent: ${e.type}\ndata: ${JSON.stringify(e.data)}\n\n`).join(''));
      if (!hold) res.end();
    } else {
      res.writeHead(404, {'content-type': 'application/json'});
      res.end('{}');
    }
  };
  return {counts, handler};
}

async function withServer(handler, run) {
  const server = createServer(handler);
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  try { return await run(`http://127.0.0.1:${server.address().port}`); }
  finally { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)); }
}

test('wait returns on a terminal stream event without polling', async () => {
  const s = scripted({states: ['running', 'succeeded'], events: [{id: '1', type: 'task.succeeded', data: {task_id: 'task1'}}]});
  await withServer(s.handler, async base => {
    const result = await new Client({baseURL: base}).wait('task1', {interval: 5000});
    assert.equal(result.reason, 'terminal');
    assert.equal(result.task.state, 'succeeded');
    assert.deepEqual(s.counts, {task: 2, actions: 0, stream: 1});
  });
});

test('wait requery timer covers the subscription race', async () => {
  const s = scripted({states: ['running', 'succeeded'], events: []}); // stream stays silent
  await withServer(s.handler, async base => {
    assert.equal((await new Client({baseURL: base}).wait('task1', {interval: 50})).reason, 'terminal');
    assert.deepEqual(s.counts, {task: 2, actions: 0, stream: 1});
  });
});

test('wait falls back to backoff polling when the stream dies', async () => {
  const s = scripted({states: ['running', 'running', 'succeeded'], hold: false});
  await withServer(s.handler, async base => {
    assert.equal((await new Client({baseURL: base}).wait('task1', {interval: 30})).reason, 'terminal');
    assert.equal(s.counts.task, 3);
    assert.ok(s.counts.stream >= 2, `stream attempts ${s.counts.stream}`);
  });
});

test('wait aborts via signal', async () => {
  const s = scripted({states: ['running'], events: []});
  await withServer(s.handler, async base => {
    const controller = new AbortController();
    const pending = new Client({baseURL: base}).wait('task1', {interval: 60000, signal: controller.signal, timeout: 60000});
    setTimeout(() => controller.abort('stop-it'), 20);
    await assert.rejects(pending, error => error === 'stop-it');
  });
});

test('wait tool event triggers an action return', async () => {
  const s = scripted({
    states: ['running', 'waiting'],
    actions: [{task_id: 'task1', id: 'act1'}],
    events: [{id: '1', type: 'tool.call', data: {task_id: 'task1'}}],
  });
  await withServer(s.handler, async base => {
    const result = await new Client({baseURL: base}).wait('task1', {interval: 5000});
    assert.equal(result.reason, 'action');
    assert.equal(result.required_actions.length, 1);
    assert.deepEqual(s.counts, {task: 2, actions: 1, stream: 1});
  });
});

test('wait ignores delta events until the requery timer fires', async () => {
  const s = scripted({
    states: ['running', 'succeeded'],
    events: [1, 2, 3].map(n => ({id: String(n), type: 'message.delta', data: {task_id: 'task1'}})),
  });
  await withServer(s.handler, async base => {
    assert.equal((await new Client({baseURL: base}).wait('task1', {interval: 60})).reason, 'terminal');
    assert.deepEqual(s.counts, {task: 2, actions: 0, stream: 1});
  });
});

test('browser fetch receiver and explicit JSON transport',async()=>{
 const browserFetch=async function(url){assert.equal(this,undefined);assert.match(url,/\/v1\/tasks\/task-test\/trace$/);return new Response(JSON.stringify({version:1,spans:[]}));};
 const sdk=new Client({baseURL:'http://localhost:8080',fetch:browserFetch});
 assert.deepEqual((await sdk.call('executionTrace',{path:{id:'task-test'}})).data,{version:1,spans:[]});
});
