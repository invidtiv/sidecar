// Run the served pairing script against the real API with native WebCrypto.
// IndexedDB is a structured-clone test adapter here; the separate browser
// proof uses real Chromium IndexedDB when a Playwright module is supplied.
import { readFileSync } from 'node:fs';
import { webcrypto } from 'node:crypto';

const [page,link]=process.argv.slice(2);
const script=readFileSync(page,'utf8').match(/<script>([\s\S]*)<\/script>/)?.[1];
if(!script)throw Error('no pairing script');
const url=new URL(link);
const records=new Map();
let cleared=false,navigated=null,status='';
if(!globalThis.crypto)Object.defineProperty(globalThis,"crypto",{value:webcrypto});
globalThis.localStorage={removeItem(key){if(key!=='sidecar.session')throw Error('unexpected storage mutation');cleared=true;},setItem(){throw Error('bearer written to localStorage');}};
globalThis.indexedDB={open(){const request={};queueMicrotask(()=>{request.result={createObjectStore(){},close(){},transaction(){const tx={objectStore(){return {put(record,key){records.set(key,structuredClone(record));queueMicrotask(()=>tx.oncomplete?.());}}}};return tx;}};request.onupgradeneeded?.();request.onsuccess?.();});return request;}};
const done=new Promise(resolve=>{
 globalThis.location={hash:url.hash,pathname:url.pathname,origin:url.origin,replace(target){navigated=target;resolve();}};
 globalThis.history={replaceState(){location.hash='';}};
 globalThis.document={getElementById(){return {set textContent(value){status=value;resolve();}}}};
});
const realFetch=globalThis.fetch;
globalThis.fetch=(path,init)=>realFetch(new URL(path,url.origin),{...init,headers:{...init.headers,Origin:url.origin}});
new Function(script)();await done;
if(!navigated)throw Error('pairing stopped: '+status);
const record=records.get('active');
if(!cleared||!record?.registration_id||record.privateKey.extractable||'token' in record)throw Error('unsafe persisted browser identity');
let exportRefused=false;try{await crypto.subtle.exportKey('jwk',record.privateKey);}catch{exportRefused=true;}
if(!exportRefused)throw Error('private key was extractable');
const post=async(path,body)=>{const response=await fetch(path,{method:'POST',headers:{'Content-Type':'application/json','X-Sidecar-Request':'1'},body:JSON.stringify(body)});const data=await response.json();if(!response.ok)throw Error(JSON.stringify(data));return data;};
const challenge=await post('/api/v0/pairing/session-proof',{registration_id:record.registration_id});
const message=['sidecar-browser-session-v1',url.origin,record.registration_id,challenge.nonce,String(challenge.timestamp)].join('\n');
const sig=await crypto.subtle.sign({name:'ECDSA',hash:'SHA-256'},record.privateKey,new TextEncoder().encode(message));
const token=await post('/api/v0/pairing/session-proof/verify',{registration_id:record.registration_id,...challenge,expires_at:undefined,signature:Buffer.from(sig).toString('base64url')});
console.log(JSON.stringify({token:token.token,registration_id:record.registration_id,non_extractable:true,legacy_cleared:cleared,navigated}));
