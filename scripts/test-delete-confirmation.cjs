const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync(require('node:path').join(__dirname, '../web/filesystem.js'), 'utf8');
const start = source.indexOf('async function deleteEntry(item)');
const end = source.indexOf('\ndocument.querySelectorAll',start);
async function run(directory, answers) {
 const requests=[],questions=[],messages=[];let refreshes=0;
 const context={mutationPending:false,confirm(message){questions.push(message);return answers.shift();},
  async api(url,body){requests.push(body);if(directory&&!body.recursive)throw Object.assign(new Error('confirm'),{requiresConfirmation:true});},
  notify(message){messages.push(message);},async loadDirectory(){refreshes++;}};
 vm.createContext(context);vm.runInContext(source.slice(start,end),context);
 await context.deleteEntry({name:'example',path:'example',directory});
 assert.equal(context.mutationPending,false);
 return {requests,questions,messages,refreshes};
}
(async()=>{
 let result=await run(true,[false]);assert.equal(result.requests.length,0);
 result=await run(true,[true,false]);assert.equal(result.questions.length,2);assert.equal(result.requests.length,1);assert.equal(result.refreshes,0);assert.equal(result.messages.length,0);
 result=await run(true,[true,true]);assert.equal(result.questions.length,2);assert.equal(result.requests.length,2);assert.equal(result.requests[1].recursive,true);assert.equal(result.refreshes,1);
 result=await run(false,[true]);assert.equal(result.questions.length,1);assert.equal(result.requests.length,1);
 console.log('Delete confirmation checks passed: cancel first/second prompts, confirmed recursion and ordinary files.');
})().catch(error=>{console.error(error);process.exitCode=1;});
