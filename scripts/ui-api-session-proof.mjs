// Browser proof-of-possession through the real CLI and HTTP service. Set
// SIDECAR_PROOF_PLAYWRIGHT_MODULE to @playwright/test to use real IndexedDB.
import assert from 'node:assert/strict';
import { spawn, execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { readFile, writeFile, rm, mkdir, symlink, rename } from 'node:fs/promises';
import { openSync, closeSync } from 'node:fs';
import { webcrypto } from 'node:crypto';
import { createRequire } from 'node:module';
import { once } from 'node:events';
const run=promisify(execFile),root=process.argv[2],bin=root+'/bin/sidecar',config=root+'/config.json';
let server,browser,context,port=0,base;
async function sc(...args){return (await run(bin,['-config',config,...args])).stdout;}
async function start(){
 const output=openSync(root+'/serve.out','w',0o600),error=openSync(root+'/serve.err','w',0o600);
 server=spawn(bin,['-config',config,'api','serve','--fixtures','testdata/ui-api/v0','--ui',root+'/ui','--port',String(port),'--json'],{stdio:['ignore',output,error]});closeSync(output);closeSync(error);
 for(let i=0;i<100;i++){
  if(server.exitCode!==null)throw Error('startup: '+await readFile(root+'/serve.err','utf8'));
  const line=await readFile(root+'/serve.out','utf8');if(line.trim()){const endpoint=JSON.parse(line);port=Number(endpoint.tcp.split(':').at(-1));base='http://'+endpoint.tcp;return;}
  await new Promise(resolve=>setTimeout(resolve,100));
 }
 throw Error('startup timed out');
}
async function stop(){if(!server)return;const child=server;server=null;if(child.exitCode===null){const exited=once(child,'exit');child.kill('SIGTERM');const [code]=await exited;assert.equal(code,0,'clean shutdown');}}
async function post(path,body){const response=await fetch(base+path,{method:'POST',headers:{Origin:base,'Content-Type':'application/json','X-Sidecar-Request':'1'},body:JSON.stringify(body)});const data=await response.json();if(!response.ok)throw Error(JSON.stringify(data));return data;}
async function status(token){return (await fetch(base+'/api/v0/hello',{headers:{Origin:base,Authorization:'Bearer '+token}})).status;}
async function pair(){
 const link=(await sc('api','open','--print')).trim();
 if(context){
  const page=await context.newPage();await page.goto(base);await page.evaluate(()=>localStorage.setItem('sidecar.session','LEGACY_BEARER_TO_CLEAR'));
  await page.goto(link);await page.waitForURL(base+'/',{timeout:10000});
  const id=await page.evaluate(async()=>{
   const db=await new Promise((resolve,reject)=>{const r=indexedDB.open('sidecar-browser-auth',1);r.onsuccess=()=>resolve(r.result);r.onerror=()=>reject(r.error);});
   const record=await new Promise((resolve,reject)=>{const r=db.transaction('keys','readonly').objectStore('keys').get('active');r.onsuccess=()=>resolve(r.result);r.onerror=()=>reject(r.error);});db.close();
   if(localStorage.getItem('sidecar.session')!==null || record.privateKey.extractable || 'token' in record)throw Error('bearer or extractable key persisted');
   let refused=false;try{await crypto.subtle.exportKey('jwk',record.privateKey);}catch{refused=true;}if(!refused)throw Error('private key exported');
   window.proofRegistration=record;return record.registration_id;
  });
  return {id,page};
 }
 const keys=await webcrypto.subtle.generateKey({name:'ECDSA',namedCurve:'P-256'},false,['sign','verify']);
 const public_key=await webcrypto.subtle.exportKey('jwk',keys.publicKey);
 const response=await post('/api/v0/pairing/exchange',{code:new URLSearchParams(new URL(link).hash.slice(1)).get('code'),public_key});
 await assert.rejects(webcrypto.subtle.exportKey('jwk',keys.privateKey));
 return {id:response.registration_id,keys:structuredClone(keys)};
}
async function renew(registration){
 if(registration.page)return registration.page.evaluate(async()=>{
  const record=window.proofRegistration;
  const post=async(path,body)=>{const r=await fetch(path,{method:'POST',headers:{'Content-Type':'application/json','X-Sidecar-Request':'1'},body:JSON.stringify(body),credentials:'omit',cache:'no-store'});const data=await r.json();if(!r.ok)throw Error(JSON.stringify(data));return data;};
  const c=await post('/api/v0/pairing/session-proof',{registration_id:record.registration_id});
  const message=['sidecar-browser-session-v1',location.origin,record.registration_id,c.nonce,String(c.timestamp)].join('\n');
  const sig=await crypto.subtle.sign({name:'ECDSA',hash:'SHA-256'},record.privateKey,new TextEncoder().encode(message));
  const signature=btoa(String.fromCharCode(...new Uint8Array(sig))).replace(/\+/g,'-').replace(/\//g,'_').replace(/=+$/,'');
  const bearer=await post('/api/v0/pairing/session-proof/verify',{registration_id:record.registration_id,nonce:c.nonce,timestamp:c.timestamp,signature});
  return bearer.token; // returned to proof memory only, never browser storage
 });
 const c=await post('/api/v0/pairing/session-proof',{registration_id:registration.id});
 const message=['sidecar-browser-session-v1',base,registration.id,c.nonce,String(c.timestamp)].join('\n');
 const sig=await webcrypto.subtle.sign({name:'ECDSA',hash:'SHA-256'},registration.keys.privateKey,new TextEncoder().encode(message));
 return (await post('/api/v0/pairing/session-proof/verify',{registration_id:registration.id,nonce:c.nonce,timestamp:c.timestamp,signature:Buffer.from(sig).toString('base64url')})).token;
}
process.once("SIGTERM", async()=>{try{await browser?.close();await stop();}finally{process.exit(143);}});
try{
 if(process.env.SIDECAR_PROOF_PLAYWRIGHT_MODULE){const {chromium}=createRequire(import.meta.url)(process.env.SIDECAR_PROOF_PLAYWRIGHT_MODULE);browser=await chromium.launch({headless:true});context=await browser.newContext();}
 await start();const first=await pair(),second=await pair();let token=await renew(first),other=await renew(second);assert.equal(await status(token),200);
 const store=await readFile(root+'/state/sidecar/api/sessions.json','utf8');assert(!store.includes(token));assert(!store.includes(other));assert(!store.includes('token_sha256'));assert(store.includes('public_key'));
 console.log('== rebuilt UI and confined symlink');await rm(root+'/ui',{recursive:true});await mkdir(root+'/ui');await writeFile(root+'/ui/index.html','NEW_UI');await symlink(root+'/secret',root+'/ui/escape');assert.equal(await (await fetch(base+'/s/aerie/demo')).text(),'NEW_UI');assert.equal(await (await fetch(base+'/escape')).text(),'NEW_UI');
 console.log('== restart invalidates bearers; existing key signs a fresh nonce');await stop();await start();assert.equal(await status(token),401);assert.equal(await status(other),401);token=await renew(first);other=await renew(second);assert.equal(await status(token),200);assert.equal(await status(other),200);
 if(context){
  // A fresh document reads the actual persisted IndexedDB key, not an old JS variable.
  const page=await context.newPage();await page.goto(base);const id=await page.evaluate(async()=>{const db=await new Promise(resolve=>{const r=indexedDB.open('sidecar-browser-auth',1);r.onsuccess=()=>resolve(r.result);});const record=await new Promise(resolve=>{const r=db.transaction('keys','readonly').objectStore('keys').get('active');r.onsuccess=()=>resolve(r.result);});db.close();window.proofRegistration=record;return record.registration_id;});assert.equal(id,second.id);assert.equal(await status(await renew({id,page})),200);
 }
 console.log('== executable upgrade and key renewal');const exited=once(server,'exit');await symlink(root+'/bin/second',root+'/bin/sidecar.next');await rename(root+'/bin/sidecar.next',bin);const [code]=await exited;assert.equal(code,0);server=null;assert.match(await readFile(root+'/serve.err','utf8'),/executable changed/);await start();assert.equal(await status(token),401);token=await renew(first);assert.equal(await status(token),200);
 console.log('== durable CLI session revocation');const revoked=JSON.parse(await sc('api','pair','--revoke-sessions','--origin',base,'--json'));assert.equal(revoked.revoked,2);await stop();await start();await assert.rejects(renew(first));await assert.rejects(renew(second));
 console.log('== durable CLI origin revocation');const third=await pair();token=await renew(third);await sc('api','pair','--origin',base,'--json');await sc('api','pair','--revoke',base,'--json');await stop();await start();assert.equal(await status(token),401);await assert.rejects(renew(third));
 console.log('ui-api-session-proof: PASS ('+(context?'Chromium WebCrypto + real IndexedDB':'native WebCrypto')+')');
}finally{await browser?.close();await stop();}
