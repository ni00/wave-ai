import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import {Client, APIError, generated, parseJSON} from '../dist/index.js';

const fixtures=JSON.parse(readFileSync(new URL('../../../tests/clients/fixtures.json',import.meta.url),'utf8'));
const base=process.env.WAVE_CLIENT_TEST_URL;
const client=scenario=>new Client({baseURL:`${base}/ts/${scenario}`,apiKey:'Bearer '+fixtures.key});
test('null and unsafe numbers',()=>{
  assert.deepEqual(generated.ModelclientUsageToJSON(generated.ModelclientUsageFromJSON(fixtures.usage)),fixtures.usage);
  assert.throws(()=>parseJSON('{"sequence":9007199254740993}'),RangeError);
});
test('shared HTTP contracts',{skip:!base},async()=>{
  await assert.rejects(client('error').call('agentsList'),e=>e instanceof APIError&&e.code==='permission_error'&&e.requestID==='req_contract');
  await assert.rejects(new generated.AgentsApi(client('error').rawConfiguration()).agentsList(),e=>e.response.status===403);
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
