const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const source = name => fs.readFileSync(path.join(__dirname, '../web', name), 'utf8');
function node() {
 return {handlers:{},classList:{toggle(){}},setAttribute(){},focus(){this.focused=true;},
  addEventListener(name,fn){this.handlers[name]=fn;}};
}
const tick = () => new Promise(resolve => setImmediate(resolve));
async function testDialog() {
 const dialogs=[];
 const prior={isConnected:true,focus(){this.focused=true;}};
 const context={window:{},document:{activeElement:prior,body:{append(el){dialogs.push(el);}},createElement(){
  const el=node(),title=node(),message=node(),cancel=node(),accept=node();
  el.querySelector=s=>s==='h2'?title:message;
  el.querySelectorAll=()=>[cancel,accept];
  el.showModal=()=>{el.open=true;};
  el.close=()=>{el.open=false;el.handlers.close();};
  el.remove=()=>{el.removed=true;};
  Object.assign(el,{title,message,cancel,accept});return el;
 }}};
 vm.runInNewContext(source('dialog.js'),context);
 const confirm=context.window.LanCopyDialog.confirm;
 const first=confirm({message:'<img src=x onerror=alert(1)>',danger:true});
 const second=confirm({message:'second'});
 await tick();assert.equal(dialogs.length,1);
 assert.equal(dialogs[0].message.textContent,'<img src=x onerror=alert(1)>');
 dialogs[0].cancel.handlers.click();assert.equal(await first,false);
 await tick();assert.equal(dialogs.length,2);assert.equal(dialogs[0].removed,true);assert(prior.focused);
 dialogs[1].accept.handlers.click();assert.equal(await second,true);
 const third=confirm({message:'Esc'});await tick();
 // A native <dialog> Escape dismissal dispatches close without acceptance.
 dialogs[2].close();assert.equal(await third,false);
}
async function testShutdown() {
 const button=node(),hint=node(),events=[],requests=[];
 let answer=false,resolveAnswer;
 const context={window:{LanCopyDialog:{confirm:()=>new Promise(resolve=>{resolveAnswer=resolve;})},
  LanCopyFileUI:{notify(){}},dispatchEvent:event=>events.push(event.type)},
  $:s=>s==='#shutdownBtn'?button:hint,modal:{open:true},zone:{},startupToggle:{},Event,
  fetch:async(url,options)=>{requests.push({url,options});return {ok:answer,json:async()=>({error:'拒绝关闭'})};}};
 vm.createContext(context);
 const settings=source('settings.js');
 vm.runInContext(settings.slice(settings.indexOf(' let info,'),settings.indexOf(" const zone ="))+
  '\nfunction setInfo(value) { info=value; showShutdownAvailability(); }',context);
 context.setInfo({local:false});assert.equal(button.disabled,true);
 await button.handlers.click();assert.equal(requests.length,0);
 context.setInfo({local:true});assert.equal(button.disabled,false);
 let pending=button.handlers.click();await tick();assert.equal(button.disabled,true);
 await button.handlers.click();resolveAnswer(false);await pending;
 assert.equal(requests.length,0);assert.equal(button.disabled,false);assert(button.focused);
 pending=button.handlers.click();await tick();resolveAnswer(true);await pending;
 assert.equal(requests.length,1);assert.equal(button.disabled,false);assert.equal(hint.textContent,'拒绝关闭');
 answer=true;pending=button.handlers.click();await tick();resolveAnswer(true);await pending;
 assert.equal(requests.length,2);assert.equal(button.disabled,true);
 assert.equal(requests[1].options.headers['X-Lan-Copy-Action'],'shutdown');
 assert.deepEqual(events,['lan-copy:shutdown']);assert.equal(context.zone.disabled,true);
}
(async()=>{await testDialog();await testShutdown();console.log('Dialog and shutdown checks passed: queue, cancel, Escape, safe text, local-only, duplicate clicks, failure and success.');})().catch(error=>{console.error(error);process.exitCode=1;});
