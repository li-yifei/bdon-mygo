import {test} from 'node:test';
import assert from 'node:assert/strict';
import vm from 'node:vm';
import fs from 'node:fs';
import {webcrypto} from 'node:crypto';
const script=fs.readFileSync(new URL('./inject.js', import.meta.url),'utf8');
function fixture(messages){
 const calls=[]; class OriginalWorker{constructor(url){this.url=url;}}
 const context={URL,EventTarget,MessageEvent,Request,TextDecoder,TextEncoder,crypto:webcrypto,location:{origin:'https://bdon.moe',href:'https://bdon.moe/tools/deck'},Worker:OriginalWorker,fetch:async()=>new Response('{}'),Response, mygo:{call:async(method,...args)=>{calls.push([method,...args]);if(method.endsWith('Prepare'))return {datasetId:args[1],initMs:1,capabilitiesJson:'{}'};return '{"format":"ournotes-deck.account-recommendation/1"}';}}};
 const bridge=context.mygo.call;context.mygo.call=async(method,...args)=>method.endsWith('UseNativeSolver')?true:method.endsWith('RecommendStart')?'':bridge(method,...args);
 context.setTimeout=setTimeout;
 if(messages)context.__bdonMygoMessages=messages;
 context.window=context;context.top=context;vm.createContext(context);vm.runInContext(script,context);return {context,calls,OriginalWorker};
}
test('deck Worker uses native protocol and preserves exact JSON',async()=>{
 const {context,calls}=fixture();const worker=new context.Worker('/deck/deck-worker.js');const events=[];worker.onmessage=e=>events.push(e.data);
 const sha=Buffer.from(await webcrypto.subtle.digest('SHA-256',new TextEncoder().encode('{}'))).toString('hex');
 worker.postMessage({type:'init',protocol:'moenotes.deck-worker/1',deckDataUrl:'https://storage.bdon.moe/data.json',deckDataBytes:2,deckDataSha256:sha,modelCommit:null});await worker.ready;
 const account='{"id":9223372036854775807}';worker.postMessage({type:'run',jobId:'job-1',inputRevision:7,accountJson:account,requestJson:'{}'});await worker.queue;
 assert.equal(events[0].type,'ready');assert.equal(events[1].jobId,'job-1');assert.equal(events[1].inputRevision,7);assert.equal(calls[1][2],account);
 worker.terminate();worker.emit({type:'result'});assert.equal(events.length,2);
});
test('unrelated Workers keep browser implementation',()=>{const {context,OriginalWorker}=fixture();assert.ok(new context.Worker('/other.js') instanceof OriginalWorker);});
test('unknown protocol fails explicitly',async()=>{const {context}=fixture();const worker=new context.Worker('/deck/deck-worker.js');const events=[];worker.onmessage=e=>events.push(e.data);worker.postMessage({type:'init',protocol:'unknown'});await assert.rejects(worker.ready);assert.equal(events[0].type,'failed');});
test('shell messages follow __bdonMygoMessages with English fallback',async()=>{
 for(const [messages,want] of [[undefined,'The page calculation protocol has changed; please update the desktop app'],[{protocolChanged:'ページの計算プロトコルが変更されました'},'ページの計算プロトコルが変更されました']]){
  const {context}=fixture(messages);const worker=new context.Worker('/deck/deck-worker.js');const events=[];worker.onmessage=e=>events.push(e.data);
  worker.postMessage({type:'init',protocol:'unknown'});await assert.rejects(worker.ready);assert.equal(events[0].message,'Error: '+want);
 }
 const {context}=fixture({downloadFailed:'下载失败：{status}'});
 context.fetch=async()=>new Response('',{status:503});
 const worker=new context.Worker('/deck/deck-worker.js');worker.onmessage=()=>{};
 worker.postMessage({type:'init',protocol:'moenotes.deck-worker/1',deckDataUrl:'https://storage.bdon.moe/data.json'});
 await assert.rejects(worker.ready,/下载失败：503/);
});

test('title bar keeps clear of the window controls via mygo inset variables', async () => {
  const { readFile } = await import('node:fs/promises');
  const source = await readFile(new URL('./window.js', import.meta.url), 'utf8');
  assert.match(source, /var\(--mygo-titlebar-inset-left, 0px\)/);
  assert.match(source, /var\(--mygo-titlebar-inset-right, 0px\)/);
  assert.doesNotMatch(source, /--mygo-titlebar-(left|right)\b/);
});
test('an engine of another model than the page keeps the page solver',async()=>{
 const sha=Buffer.from(await webcrypto.subtle.digest('SHA-256',new TextEncoder().encode('{}'))).toString('hex');
 const init={type:'init',protocol:'moenotes.deck-worker/1',deckDataUrl:'https://storage.bdon.moe/data.json',deckDataBytes:2,deckDataSha256:sha,modelCommit:'page'};
 for(const [failure,fallback] of [['Error: engine-model-mismatch: the engine implements a, the page b',true],['Error: disk full',false]]){
  const {context,OriginalWorker}=fixture();const posted=[];OriginalWorker.prototype.postMessage=function(m){posted.push(m);};
  const call=context.mygo.call;context.mygo.call=async(method,...args)=>{if(method.endsWith('Prepare'))throw new Error(failure);return call(method,...args);};
  const worker=new context.Worker('/deck/deck-worker.js');const events=[];worker.onmessage=e=>events.push(e.data);
  worker.postMessage(init);
  if(fallback){await worker.ready;assert.ok(worker.delegate instanceof OriginalWorker);assert.equal(posted[0],init);assert.equal(context.ournotesNative.engine,'bdon-original');assert.match(context.ournotesNative.lastFallbackError,/engine-model-mismatch/);}
  else{await assert.rejects(worker.ready,/disk full/);assert.equal(events[0].type,'failed');assert.equal(worker.delegate,null);}
 }
});
