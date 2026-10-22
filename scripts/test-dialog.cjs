const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const source = name => fs.readFileSync(path.join(__dirname, '../web', name), 'utf8');
function node() {
 return {handlers:{},classList:{toggle(){}},disabled:false,value:'',setAttribute(){},focus(){this.focused=true;},
  addEventListener(name,fn){this.handlers[name]=fn;}};
}
const tick = () => new Promise(resolve => setImmediate(resolve));
async function testDialog() {
 const dialogs=[];
 const prior={isConnected:true,focus(){this.focused=true;}};
 const context={window:{},document:{activeElement:prior,body:{append(el){dialogs.push(el);}},createElement(){
  const el=node(),title=node(),message=node(),cancel=node(),accept=node(),input=node(),strong=node();
  el.querySelector=s=>s==='h2'?title:s==='p'?message:s==='input'?input:strong;
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
 const typedPromise=context.window.LanCopyDialog.confirmText({message:'Risk',requiredText:'确认关闭'});await tick();
 const typedDialog=dialogs[3],typedInput=typedDialog.querySelector('input'),typedAccept=typedDialog.querySelectorAll()[1];
 typedAccept.disabled=true;assert.equal(typedAccept.disabled,true);typedInput.value='wrong';typedInput.handlers.input();assert.equal(typedAccept.disabled,true);
 typedInput.value='确认关闭';typedInput.handlers.input();assert.equal(typedAccept.disabled,false);
 typedAccept.handlers.click();assert.equal(await typedPromise,true);
}
async function testShutdown() {
 const button=node(),hint=node(),events=[],requests=[];
 const confirmResolvers=[],typedResolvers=[];
 let posts=0;
 const context={window:{LanCopyDialog:{confirm:()=>new Promise(resolve=>confirmResolvers.push(resolve)),confirmText:()=>new Promise(resolve=>typedResolvers.push(resolve))},
  LanCopyFileUI:{notify(){}},dispatchEvent:event=>events.push(event.type)},
  $:s=>s==='#shutdownBtn'?button:hint,modal:{open:true},zone:{},startupToggle:{},Event,
  showReleaseAvailability(){},
  fetch:async(url,options)=>{requests.push({url,options});if(url==='/api/shutdown/status')return {ok:true,json:async()=>({uploads:2,downloads:1,stopping:false})};if(posts++===0)return {ok:false,status:409,json:async()=>({requiresConfirmation:true,uploads:3,downloads:1})};return {ok:true,status:202,json:async()=>({status:'shutting_down'})};}};
 vm.createContext(context);
 const settings=source('settings.js');
 vm.runInContext(settings.slice(settings.indexOf(' let info,'),settings.indexOf(" const zone ="))+
  '\nfunction setInfo(value) { info=value; showShutdownAvailability(); }',context);
 context.setInfo({local:false,canShutdown:false});assert.equal(button.disabled,true);
 await button.handlers.click();assert.equal(requests.length,0);
 context.setInfo({local:true,canShutdown:true});assert.equal(button.disabled,false);
 let pending=button.handlers.click();await tick();assert.equal(button.disabled,true);
 await button.handlers.click();confirmResolvers.shift()(false);await pending;
 assert.equal(requests.length,1);assert.equal(button.disabled,false);assert(button.focused);
 pending=button.handlers.click();await tick();confirmResolvers.shift()(true);await tick();typedResolvers.shift()(true);await tick();
 await tick();confirmResolvers.shift()(true);await tick();typedResolvers.shift()(true);await pending;
 assert.equal(requests.length,4);assert.equal(requests[0].url,'/api/shutdown/status');assert.equal(button.disabled,true);
 assert.equal(requests[2].options.headers['X-Lan-Copy-Action'],'shutdown');
 assert.equal(JSON.parse(requests[2].options.body).confirmation,'强制关闭');
 assert.deepEqual(JSON.parse(requests[2].options.body),{confirmation:'强制关闭',uploads:2,downloads:1});
 assert.deepEqual(JSON.parse(requests[3].options.body),{confirmation:'强制关闭',uploads:3,downloads:1});
 assert.deepEqual(events,['lan-copy:shutdown']);assert.equal(context.zone.disabled,true);
}
(async()=>{await testDialog();await testShutdown();console.log('Dialog and shutdown checks passed: queue, cancel, Escape, safe text, local-only, duplicate clicks, failure and success.');})().catch(error=>{console.error(error);process.exitCode=1;});
