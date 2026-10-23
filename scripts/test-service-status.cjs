const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
function node() {
 const classes = new Set();
 return {handlers: new Map(), attrs:{}, hidden:true, style:{}, offsetWidth:260, textContent:'',
  classList:{add:c=>classes.add(c),remove:c=>classes.delete(c),contains:c=>classes.has(c)},
  setAttribute(k,v){this.attrs[k]=v;},
  addEventListener(k,f){this.handlers.set(k,f);},
  contains(target){return target===this;},
  getBoundingClientRect(){return {right:1140,bottom:42};}
 };
}
const nodes = Object.fromEntries(['shutdownBtn','serviceStatus','statusAccess','serviceStatusPopover','dataUpdatedAt','dataRefreshHint'].map(id=>[id,node()]));
const label=node();nodes.serviceStatus.querySelector=()=>label;
const events = new Map();
const document={querySelector:s=>nodes[s.slice(1)],addEventListener(){}};
const window={innerWidth:1280,addEventListener:(name,fn)=>events.set(name,fn)};
vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../web/shell.js'),'utf8'),{document,window,Date,clearTimeout,setTimeout});
const emit=name=>events.get(name)();
emit('lan-copy:data-updated');
const saved=nodes.dataUpdatedAt.dateTime;
assert(!Number.isNaN(Date.parse(saved)));
assert.equal(label.textContent,'局域网在线');
assert.equal(nodes.dataRefreshHint.textContent,'文件和文本变化时实时更新');
nodes.statusAccess.handlers.get('pointerenter')({pointerType:'mouse'});
assert.equal(nodes.serviceStatusPopover.hidden,false);
assert.equal(nodes.serviceStatus.attrs['aria-expanded'],'true');
emit('lan-copy:data-failed');
assert.equal(nodes.dataUpdatedAt.dateTime,saved);
assert.equal(label.textContent,'连接异常');
assert.equal(nodes.serviceStatus.classList.contains('offline'),true);
emit('lan-copy:data-updated');
assert.equal(nodes.serviceStatus.classList.contains('offline'),false);
assert.equal(label.textContent,'局域网在线');
const lastSuccess=nodes.dataUpdatedAt.dateTime;
emit('lan-copy:shutdown');
emit('lan-copy:data-updated');
assert.equal(nodes.dataUpdatedAt.dateTime,lastSuccess);
 assert.equal(nodes.dataRefreshHint.textContent,'服务已关闭，已停止自动更新');

const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
function eventSourceFixture() {
 class FakeEventSource {
  static OPEN = 1;
  constructor(){this.readyState=0;this.listeners=new Map();this.closed=false;FakeEventSource.instance=this;}
  addEventListener(name,fn){this.listeners.set(name,fn);}
  emit(name,data){this.listeners.get(name)?.({data:JSON.stringify(data)});}
  close(){this.closed=true;}
 }
 return FakeEventSource;
}
async function testDirectoryRefreshCancellationAndEvents() {
 const source=fs.readFileSync(path.join(__dirname,'../web/filesystem.js'),'utf8');
 const loadStart=source.indexOf('async function loadDirectory()'),loadEnd=source.indexOf('\nfunction renderEntries()',loadStart);
 const eventsStart=source.indexOf('function scheduleDirectoryRefresh()'),eventsEnd=source.indexOf('\nloadDirectory();',eventsStart)+'\nloadDirectory();'.length;
 const signals=[],handlers=new Map(),FakeEventSource=eventSourceFixture();let reads=0,pendingSignal,blockNext=false,timer,cleared=[];
 const fixture={URLSearchParams,Event,AbortController,requestVersion:0,currentPath:'',lastListing:'',entries:[],stopped:false,directoryController:null,eventRefreshTimer:null,fallbackRefreshTimer:null,searchTimer:null,events:null,document:{hidden:false,addEventListener:(n,f)=>handlers.set(n,f)},
  $:s=>s==='#search'?{value:''}:{textContent:'',replaceChildren(){},classList:{add(){}}},window:{EventSource:FakeEventSource,addEventListener:(n,f)=>handlers.set(n,f),dispatchEvent:e=>signals.push(e.type)},EventSource:FakeEventSource,
  api:(url,data,signal)=>{reads++;if(blockNext){blockNext=false;return new Promise((_,reject)=>{pendingSignal=signal;signal.addEventListener('abort',()=>reject(Object.assign(new Error('aborted'),{name:'AbortError'})));});}return Promise.resolve({entries:[]});},crumbs(){},renderEntries(){},notify(){},navigate(){},
  setInterval(fn,delay){timer={fn,delay};return 42;},clearInterval(id){cleared.push(id);},setTimeout,clearTimeout};
 vm.createContext(fixture);vm.runInContext(source.slice(loadStart,loadEnd)+'\n'+source.slice(eventsStart,eventsEnd),fixture);
 await sleep(0);assert.equal(reads,1);
 const es=FakeEventSource.instance;es.onerror();assert.equal(timer.delay,60000);
 timer.fn();await sleep(130);assert.equal(reads,2);
 es.readyState=FakeEventSource.OPEN;es.onopen();await sleep(130);assert.equal(reads,3);
 es.emit('change',{version:1,files:false,texts:true});await sleep(130);assert.equal(reads,3);
 es.emit('change',{version:2,files:true,texts:false});es.emit('change',{version:3,files:true,texts:false});await sleep(130);assert.equal(reads,4,'bursty file events should coalesce');
 blockNext=true;const pending=fixture.loadDirectory();fixture.document.hidden=true;handlers.get('visibilitychange')();assert.equal(pendingSignal.aborted,true);await pending;
 es.emit('change',{version:4,files:true,texts:false});await sleep(130);assert.equal(reads,5);
 fixture.document.hidden=false;handlers.get('visibilitychange')();await sleep(130);assert.equal(reads,6);
 handlers.get('lan-copy:shutdown')();assert.equal(es.closed,true);assert.ok(cleared.includes(42));
}
async function testTextRefreshCancellationAndEvents() {
 const source=fs.readFileSync(path.join(__dirname,'../web/app.js'),'utf8');
 const loadStart=source.indexOf('async function loadTexts()'),loadEnd=source.indexOf('function renderTexts(',loadStart);
 const eventsStart=source.indexOf('function scheduleTextRefresh()'),eventsEnd=source.length;
 const nodes=new Map(),handlers=new Map(),signals=[];let reads=0,timer,cleared=[];
 const $=s=>{if(!nodes.has(s))nodes.set(s,{value:'',textContent:'',addEventListener(){},replaceChildren(){},classList:{toggle(){}}});return nodes.get(s);};
 const FakeEventSource=eventSourceFixture();let delayFetch=false,delayedSignal;
 const fixture={document:{querySelector:$,addEventListener:(n,f)=>handlers.set(n,f),hidden:false},window:{EventSource:FakeEventSource,addEventListener:(n,f)=>handlers.set(n,f),dispatchEvent:e=>signals.push(e.type)},EventSource:FakeEventSource,
  textController:null,textEventSource:null,textRefreshTimer:null,textRefreshDebounce:null,stopped:false,refreshTimer:null,AbortController,Event,Map,
  renderTexts(){},notify(){},fetch:(_url,options)=>{reads++;if(delayFetch)return new Promise((resolve,reject)=>{delayedSignal=options.signal;options.signal.addEventListener('abort',()=>reject(Object.assign(new Error('aborted'),{name:'AbortError'})));});return Promise.resolve({ok:true,json:async()=>({texts:[]})});},
  setInterval(fn,delay){timer={fn,delay};return 42;},clearInterval(id){cleared.push(id);},setTimeout,clearTimeout};
 vm.createContext(fixture);vm.runInContext(source.slice(loadStart,loadEnd)+'\n'+source.slice(eventsStart,eventsEnd),fixture);
 await fixture.loadTexts();assert.equal(reads,1);assert.deepEqual(signals,['lan-copy:data-updated']);
 const es=FakeEventSource.instance;es.onerror();assert.equal(timer.delay,60000);timer.fn();await sleep(130);assert.equal(reads,2);
 es.readyState=FakeEventSource.OPEN;es.onopen();await sleep(130);assert.equal(reads,3);
 es.emit('change',{version:1,files:true,texts:false});await sleep(130);assert.equal(reads,3);
 es.emit('change',{version:2,files:false,texts:true});es.emit('change',{version:3,files:false,texts:true});await sleep(130);assert.equal(reads,4,'bursty text events should coalesce');
 delayFetch=true;const pending=fixture.loadTexts();assert.equal(reads,5);fixture.document.hidden=true;handlers.get('visibilitychange')();assert.equal(delayedSignal.aborted,true);await pending;
 fixture.document.hidden=false;handlers.get('visibilitychange')();await sleep(130);assert.equal(reads,6);
 handlers.get('lan-copy:shutdown')();assert.equal(es.closed,true);assert.ok(cleared.includes(42));handlers.get('visibilitychange')();assert.equal(reads,6);
}
async function testDirectoryRequestAbortsPrevious() {
 const source=fs.readFileSync(path.join(__dirname,'../web/filesystem.js'),'utf8');
 const start=source.indexOf('async function loadDirectory()'),end=source.indexOf('\nfunction renderEntries()',start);
 let calls=0,signals=[];const fixture={requestVersion:0,currentPath:'',lastListing:'',entries:[],stopped:false,directoryController:null,events:null,searchTimer:null,document:{hidden:false},AbortController,URLSearchParams,Event,
  $:s=>s==='#search'?{value:''}:{textContent:'',replaceChildren(){},classList:{add(){}}},window:{dispatchEvent:e=>signals.push(e.type)},crumbs(){},renderEntries(){},notify(){},navigate(){},startFallbackRefresh(){},stopFallbackRefresh(){},setTimeout,clearTimeout,api(_url,_data,signal){calls++;if(calls===1)return new Promise((_,reject)=>signal.addEventListener('abort',()=>reject(Object.assign(new Error('aborted'),{name:'AbortError'}))));return Promise.resolve({entries:[]});}};
 vm.createContext(fixture);vm.runInContext(source.slice(start,end),fixture);
 const old=fixture.loadDirectory();const latest=fixture.loadDirectory();await Promise.all([old,latest]);assert.equal(calls,2);assert.deepEqual(signals,['lan-copy:data-updated']);
}
Promise.all([testDirectoryRefreshCancellationAndEvents(),testTextRefreshCancellationAndEvents(),testDirectoryRequestAbortsPrevious()]).then(()=>console.log('Status and refresh checks passed: unified SSE changes, event coalescing, request cancellation, fallback polling, hidden pages and shutdown.')).catch(error=>{console.error(error);process.exitCode=1;});
