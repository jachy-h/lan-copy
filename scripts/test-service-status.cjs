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
assert.equal(nodes.dataRefreshHint.textContent,'每分钟自动更新');
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

// Run the real loaders to verify successful unchanged data still counts as a
// refresh, while failed and superseded requests do not advance the timestamp.
async function testDirectoryRefresh() {
 const source=fs.readFileSync(path.join(__dirname,'../web/filesystem.js'),'utf8');
 const start=source.indexOf('async function loadDirectory()');
 const end=source.indexOf('\nfunction renderEntries()',start);
 const signals=[];
 const fixture={requestVersion:0,currentPath:'',lastListing:'',entries:[],URLSearchParams,Event,
  $:s=>s==='#search'?{value:''}:{textContent:'',replaceChildren(){},classList:{add(){}}},
  window:{dispatchEvent:e=>signals.push(e.type)},
  api:async()=>({entries:[]}),crumbs(){},renderEntries(){},notify(){},navigate(){}};
 vm.createContext(fixture);vm.runInContext(source.slice(start,end),fixture);
 await fixture.loadDirectory();await fixture.loadDirectory();
 assert.deepEqual(signals,['lan-copy:data-updated','lan-copy:data-updated']);
 fixture.api=async()=>{throw new Error('offline');};
 await fixture.loadDirectory();
 assert.equal(signals.at(-1),'lan-copy:data-failed');
 const count=signals.length;
 fixture.api=async()=>{fixture.requestVersion++;return {entries:[]};};
 await fixture.loadDirectory();assert.equal(signals.length,count);
}
testDirectoryRefresh().then(()=>console.log('Status checks passed: hover, success time, unchanged data, failures, recovery, stale responses and shutdown.')).catch(error=>{console.error(error);process.exitCode=1;});

async function testTextRefreshSchedule() {
 const nodes=new Map();
 const $=s=>{if(!nodes.has(s))nodes.set(s,{value:'',textContent:'',addEventListener(){},replaceChildren(){},classList:{toggle(){}}});return nodes.get(s);};
 const handlers=new Map(), signals=[];
 let timer, cleared, reads=0;
 const fixture={document:{querySelector:$,addEventListener:(name,f)=>handlers.set(name,f),hidden:false},
  window:{addEventListener:(name,f)=>handlers.set(name,f),dispatchEvent:e=>signals.push(e.type)},
  LanCopyFileUI:{copyText(){}},TextEncoder,Event,Map,
  fetch:async()=>{reads++;return {ok:true,json:async()=>({texts:[]})};},
  setInterval(fn,delay){timer={fn,delay};return 42;},clearInterval(id){cleared=id;},
  clearTimeout(){},setTimeout(){}};
 vm.runInNewContext(fs.readFileSync(path.join(__dirname,'../web/app.js'),'utf8'),fixture);
 await new Promise(resolve=>setImmediate(resolve));
 assert.equal(timer.delay,60000);assert.equal(reads,1);assert.deepEqual(signals,['lan-copy:data-updated']);
 timer.fn();await new Promise(resolve=>setImmediate(resolve));assert.equal(reads,2);
 fixture.document.hidden=true;timer.fn();await new Promise(resolve=>setImmediate(resolve));assert.equal(reads,3);
 fixture.document.hidden=false;handlers.get('visibilitychange')();await new Promise(resolve=>setImmediate(resolve));assert.equal(reads,4);
 handlers.get('lan-copy:shutdown')();assert.equal(cleared,42);
 handlers.get('visibilitychange')();assert.equal(reads,4);
}
function testDirectoryRefreshSchedule() {
 const source=fs.readFileSync(path.join(__dirname,'../web/filesystem.js'),'utf8');
 const start=source.indexOf('let events;'),end=source.indexOf('\nloadDirectory();',start)+'\nloadDirectory();'.length;
 const handlers=new Map();let timer,cleared,reads=0;
 const fixture={window:{addEventListener:(name,f)=>handlers.set(name,f)},document:{hidden:false,addEventListener:(name,f)=>handlers.set(name,f)},
  loadDirectory(){reads++;},setInterval(fn,delay){timer={fn,delay};return 42;},clearInterval(id){cleared=id;},clearTimeout(){},searchTimer:0};
 vm.runInNewContext(source.slice(start,end),fixture);
 assert.equal(timer.delay,60000);assert.equal(reads,1);timer.fn();assert.equal(reads,2);
 handlers.get('lan-copy:shutdown')();assert.equal(cleared,42);
 handlers.get('visibilitychange')();assert.equal(reads,2);
}
Promise.all([testTextRefreshSchedule(),Promise.resolve().then(testDirectoryRefreshSchedule)]).then(()=>console.log('Both pages refresh every 60 seconds and stop after shutdown.')).catch(error=>{console.error(error);process.exitCode=1;});
