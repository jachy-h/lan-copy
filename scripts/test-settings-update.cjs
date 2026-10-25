const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
function element() {
 const handlers = new Map(), attrs = {}, classes = new Set();
 return {handlers, attrs, classes, disabled:false, hidden:false, textContent:'', value:0,
  addEventListener(name,fn) {handlers.set(name,fn);},
  setAttribute(k,v) {attrs[k]=v;},getAttribute(k) {return attrs[k];},
  classList:{add:c=>classes.add(c),remove:c=>classes.delete(c),toggle:(c,on)=>on?classes.add(c):classes.delete(c)},
  focus(){},prepend(){},showModal(){this.open=true;},close(){this.open=false;this.handlers.get('close')?.();}
 };
}
async function main() {
 const nodes = new Map();
 const get = key => {if(!nodes.has(key))nodes.set(key,element());return nodes.get(key);};
 const tabs=['about','general','update'].map(name=>{const tab=get('#'+name+'Tab');tab.id=name+'Tab';tab.setAttribute('aria-controls',name+'Settings');return tab;});
 const gear=element(),modal=element();
 modal.querySelector=get;
 modal.querySelectorAll=selector=>selector==='[role="tab"]'?tabs:[get('.settings-version')];
 let created=0, checks=0, posts=0, restarted=false, reloads=0, failed=false, available=true, startupEnabled=false;
 const document={createElement:()=>created++===0?gear:modal,querySelector:get,getElementById:id=>get('#'+id),body:{append(){}}};
 const info={version:'1.0.0',os:'windows',arch:'amd64',local:true,updateEnabled:true,signedUpdates:true};
 const calls=[];
 const fetch=async(url,opts={})=>{
  calls.push(url);
  let result;
  if(url==='/api/app')result=restarted?{...info,version:'1.1.0',updateID:'test-update'}:info;
  else if(url==='/api/settings/autostart')result={available:info.local,enabled:startupEnabled};
  else if(url==='/api/update/check') {checks++;if(failed)throw Error('GitHub unavailable');result={version:'1.1.0',comparable:true,available,canInstall:available,pageURL:'https://github.com/jachy-h/lan-copy/releases/tag/v1.1.0'};}
  else if(url==='/api/update/download'){posts++;assert.equal(opts.method,'POST');assert.equal(opts.headers['X-Lan-Copy-Action'],'update');restarted=true;result={updateID:'test-update'};}
  else throw Error(url);
  return {ok:true,status:url==='/api/update/download'?202:200,json:async()=>result};
 };
 const window={LanCopyDialog:{confirm:async()=>true},LanCopyFileUI:{notify(){}},dispatchEvent(){}};
 vm.runInNewContext(fs.readFileSync('web/settings.js','utf8'),{document,window,fetch,AbortSignal,location:{reload(){reloads++;}},setTimeout:fn=>{fn();return 0;},Event});
 const settle=async()=>{for(let i=0;i<30;i++)await Promise.resolve();};
 gear.handlers.get('click')();await settle();assert.equal(checks,1);
 tabs[2].handlers.get('click')();await settle();assert.equal(checks,2);
 assert.match(get('.release-status').textContent,/1.1.0/);
 assert.equal(get('.release-status').classes.has('success'),true);
 assert.equal(get('.release-install').hidden,false);assert.equal(get('.release-install').disabled,false);
 assert.equal(get('.release-page').hidden,false);
 get('.release-install').handlers.get('click')();
 assert.equal(get('.update-dropzone').disabled,true);
 assert.equal(get('.release-install').disabled,true);
 assert.equal(get('.release-install-status').hidden,false);
 assert.equal(get('.release-install-status').textContent,'正在下载更新包并校验，请稍候…');
 await settle();assert.equal(posts,1);assert.equal(reloads,1);
 assert.match(get('.release-install-status').textContent,/已更新至 1\.1\.0/);
 get('.settings-close').handlers.get('click')();gear.handlers.get('click')();await settle();assert.equal(checks,3);
 assert.equal(get('.release-install-status').hidden,true);
 failed=true;get('.release-check').handlers.get('click')();await settle();
 assert.match(get('.release-status').textContent,/GitHub unavailable/);
 assert.equal(get('.release-status').classes.has('success'),false);
 assert.equal(get('.release-install').hidden,true);assert.equal(get('.update-dropzone').disabled,false);
 failed=false;available=false;get('.release-check').handlers.get('click')();await settle();
 assert.match(get('.release-status').textContent,/无需更新/);
 assert.equal(get('.release-status').classes.has('success'),true);
 assert.equal(get('.release-install').hidden,true);assert.equal(get('.release-page').hidden,true);
 info.local=false;
 available=true;
 startupEnabled=true;
 gear.handlers.get('click')();await settle();
 assert.equal(get('.autostart-toggle').checked,true);
 assert.equal(get('.autostart-toggle').disabled,true);
 assert.equal(get('.release-install').hidden,false);
 assert.equal(get('.release-install').disabled,false);
 get('.release-install').handlers.get('click')();await settle();
 assert.equal(posts,2);assert.equal(reloads,2);
 startupEnabled=false;
 gear.handlers.get('click')();await settle();
 assert.equal(get('.autostart-toggle').checked,false);
 assert.equal(get('.autostart-toggle').disabled,true);
 console.log('Settings online update tests passed');
}
main().catch(e=>{console.error(e);process.exitCode=1;});
