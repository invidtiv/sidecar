#!/usr/bin/env python3
"""Exercise exact two-owner mobile routing against disposable private fixtures.

This proof is destructive only to the marked remote proof tmux server. It keeps
the marked local server alive so a reviewer can verify that the same-named local
terminal survived remote retarget and loss.
"""

import atexit,base64,datetime,hashlib,json,math,os,queue,re,statistics,subprocess,threading,time,pathlib,shlex,sys
if len(sys.argv) != 4:
 print('usage: mobile-router-proof.py LOCAL_ROOT SSH_ALIAS REMOTE_ROOT', file=sys.stderr)
 raise SystemExit(2)
root=pathlib.Path(sys.argv[1]).resolve(); remote_alias=sys.argv[2]; remote_root=sys.argv[3]
if not re.fullmatch(r'[A-Za-z0-9._-]+',remote_alias):
 raise RuntimeError('SSH alias must contain only letters, digits, dot, underscore, or hyphen')
if not str(root).startswith('/private/tmp/sidecar-mobile-router-') or not re.fullmatch(r'/private/tmp/sidecar-mobile-router-[A-Za-z0-9._-]+',remote_root):
 raise RuntimeError('proof roots must be task-specific absolute /private/tmp/sidecar-mobile-router-* paths')
marker='sidecar-mobile-router-adversarial-proof-v0'
if (root/'.sidecar-mobile-router-proof-owner').read_text().strip()!=marker:
 raise RuntimeError('local proof ownership marker missing')
remote_marker_command='cat '+shlex.quote(f'{remote_root}/.sidecar-mobile-router-proof-owner')
remote_marker=subprocess.run(['ssh',remote_alias,remote_marker_command],text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE,check=True).stdout.strip()
if remote_marker!=marker: raise RuntimeError('remote proof ownership marker missing')
remote_uid=subprocess.run(['ssh',remote_alias,'id','-u'],text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE,check=True).stdout.strip()
remote_socket=f'{remote_root}/tmux/tmux-{remote_uid}/default'
local_socket=str(root/f'tmux/tmux-{os.getuid()}/default')
binary=root/'bin'/'sidecar'; config=root/'config'/'hub.json'; original_config=config.read_bytes()
env=os.environ.copy(); env.pop('TMUX',None); env.pop('TMUX_PANE',None); env['XDG_STATE_HOME']=str(root/'state'); env['TMUX_TMPDIR']=str(root/'tmux'); env['SIDECAR_ISOLATED_STATE']='1'
records=[]; services=[]
class Service:
 def __init__(self,label):
  self.label=label; self.i=0; self.frames=[]; self.resets=[]
  self.p=subprocess.Popen([str(binary),'-config',str(config),'mobile','serve','--stdio'],stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,bufsize=1,env=env)
  services.append(self)
  self.lines=queue.Queue(); threading.Thread(target=self._read,daemon=True).start()
 def _read(self):
  for line in self.p.stdout:self.lines.put(line)
  self.lines.put(None)
 def receive(self,timeout=15):
  try: line=self.lines.get(timeout=timeout)
  except queue.Empty as e: raise RuntimeError(f'{self.label}: timed out') from e
  if line is None: return None
  m=json.loads(line); records.append({'stream':self.label,'direction':'received','type':m.get('type'),'request_id':m.get('request_id'),'output_sequence':m.get('output_sequence'),'reset_generation':m.get('reset_generation'),'error_code':(m.get('error') or {}).get('code')})
  if m['type']=='frame':self.frames.append(m)
  if m['type']=='reset':self.resets.append(m)
  return m
 def request(self,kind,expect_error=False,**fields):
  self.i+=1; rid=f'{self.label}-{self.i}'; m={'version':0,'type':kind,'request_id':rid,**fields}
  records.append({'stream':self.label,'direction':'sent','type':kind,'request_id':rid,'operation_sequence':fields.get('operation_sequence')})
  try:self.p.stdin.write(json.dumps(m,separators=(',',':'))+'\n'); self.p.stdin.flush()
  except (BrokenPipeError,ValueError):
   if expect_error:return {'type':'closed','request_id':rid}
   raise
  deadline=time.monotonic()+15
  while True:
   remaining=deadline-time.monotonic()
   if remaining<=0: raise RuntimeError(f'{self.label}: total request deadline exceeded for {kind}')
   got=self.receive(timeout=remaining)
   if got is None:
    if expect_error:return {'type':'closed','request_id':rid}
    raise RuntimeError(f'{self.label}: closed waiting for {kind}: {self.p.stderr.read()}')
   if got.get('request_id')==rid:
    if got['type']=='error' and not expect_error:raise RuntimeError(json.dumps(got))
    if expect_error and got['type']!='error':raise RuntimeError(f'{self.label}: {kind} unexpectedly succeeded')
    return got
 def frame(self,timeout=15):
  deadline=time.monotonic()+timeout
  while time.monotonic()<deadline:
   m=self.receive(timeout=max(.1,deadline-time.monotonic()))
   if m is None:raise RuntimeError(f'{self.label}: closed waiting for frame')
   if m['type']=='frame':return m
  raise RuntimeError(f'{self.label}: frame timeout')
 def close_normal(self):
  self.p.stdin.close(); code=self.p.wait(timeout=12); err=self.p.stderr.read()
  if code!=0 or err:raise RuntimeError(f'{self.label}: exit={code} stderr={err!r}')

def catalog():
 p=subprocess.run([str(binary),'-config',str(config),'mobile','sessions','--json','--sort','project'],env=env,text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=15,check=True)
 return json.loads(p.stdout)
def owner_value_remote():
 command=' '.join(shlex.quote(x) for x in ['env','-u','TMUX','-u','TMUX_PANE','/opt/homebrew/bin/tmux','-S',remote_socket,'show-options','-qv','-t','sidecar-sh-project-1','@sidecar-owner'])
 p=subprocess.run(['ssh',remote_alias,command],text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
 if p.returncode!=0: raise RuntimeError(f'remote owner read failed: {p.stderr.strip()}')
 return p.stdout.strip()
def wait_owner_empty(timeout=6):
 deadline=time.monotonic()+timeout
 while time.monotonic()<deadline:
  if not owner_value_remote():return
  time.sleep(.1)
 raise RuntimeError('remote owner lease remained')
def local_tmux(*args):
 return subprocess.run(['/opt/homebrew/bin/tmux','-S',local_socket,*args],env=env,text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE,check=True).stdout.strip()
def remote_tmux(*args,check=True):
 remote_command=' '.join(shlex.quote(x) for x in ['env','-u','TMUX','-u','TMUX_PANE','/opt/homebrew/bin/tmux','-S',remote_socket,*args])
 out=subprocess.run(['ssh',remote_alias,remote_command],text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE,check=check).stdout.strip()
 return out.splitlines()[-1].strip() if out else ''

def remote_file(path):
 p=subprocess.run(['ssh',remote_alias,'cat '+shlex.quote(path)],text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
 if p.returncode!=0: raise RuntimeError(f'remote file read failed: {p.stderr.strip()}')
 return p.stdout

def remote_socket_identity():
 command='stat -f %d:%i '+shlex.quote(remote_socket)
 p=subprocess.run(['ssh',remote_alias,command],text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
 if p.returncode!=0: raise RuntimeError(f'remote socket identity failed: {p.stderr.strip()}')
 return p.stdout.strip()

def owner_stdio_catalog(argv,command_env=None):
 p=subprocess.Popen(argv,stdin=subprocess.PIPE,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,env=command_env)
 lines=queue.Queue()
 def read_lines():
  for line in p.stdout: lines.put(line)
  lines.put(None)
 threading.Thread(target=read_lines,daemon=True).start()
 deadline=time.monotonic()+15
 def exchange(message,request_id):
  p.stdin.write(json.dumps(message,separators=(',',':'))+'\n'); p.stdin.flush()
  while True:
   remaining=deadline-time.monotonic()
   if remaining<=0: raise RuntimeError('owner catalog stdio deadline exceeded')
   try: line=lines.get(timeout=remaining)
   except queue.Empty as e: raise RuntimeError('owner catalog stdio deadline exceeded') from e
   if line is None: raise RuntimeError(f'owner catalog stdio closed: {p.stderr.read().strip()}')
   response=json.loads(line)
   if response.get('request_id')==request_id: return response
 try:
  hello=exchange({'version':0,'type':'hello','request_id':'guard-hello'},'guard-hello')
  if hello.get('type')!='hello': raise RuntimeError('owner catalog stdio hello failed')
  response=exchange({'version':0,'type':'sessions','request_id':'guard-sessions','catalog_query':{'sort':'project'}},'guard-sessions')
  if response.get('type')!='sessions' or not response.get('catalog'): raise RuntimeError(f'owner catalog stdio returned no catalog: {response!r}')
  return response['catalog']
 finally:
  try: p.stdin.close()
  except (BrokenPipeError,ValueError,OSError): pass
  try: p.wait(timeout=3)
  except subprocess.TimeoutExpired:
   p.terminate()
   try: p.wait(timeout=2)
   except subprocess.TimeoutExpired: p.kill()

def ready_private_row(snapshot):
 matches=[row for section in snapshot['sections'] for row in section['rows'] if row.get('attachment_ready') and row.get('session')=='sidecar-sh-project-1' and row.get('pane')=='%0']
 if len(matches)!=1: raise RuntimeError(f'expected one marked private owner row, got {len(matches)}')
 return matches[0]

def tmux_facts(run):
 value=run('display-message','-p','-t','%0','#{pid}|#{session_id}|#{session_created}|#{session_name}|#{pane_id}|#{pane_width}x#{pane_height}')
 parts=value.split('|')
 if len(parts)!=6: raise RuntimeError(f'unexpected private tmux facts: {value!r}')
 return dict(zip(('pid','session_id','session_created','session','pane','geometry'),parts))

def shell_created(contents,namespace):
 matches=[x for x in json.loads(contents)['shells'] if x.get('tmuxName')=='sidecar-sh-project-1' and x.get('namespace')==namespace]
 if len(matches)!=1 or not matches[0].get('createdAt'): raise RuntimeError('private durable shell identity missing or ambiguous')
 return matches[0]['createdAt']

def normalized_created(value):
 parsed=datetime.datetime.fromisoformat(value.replace('Z','+00:00')).astimezone(datetime.timezone.utc)
 return parsed.isoformat().replace('+00:00','Z')

def raw_target_generation(raw,facts,durable_created):
 expected=raw['expected_target']
 parts=(expected['workspace_id'],expected['workspace_kind'],facts['session'],facts['pane'],facts['session_id'],facts['session_created'],normalized_created(durable_created),'pid='+facts['pid'])
 return hashlib.sha256('\x00'.join(parts).encode()).digest()[:16].hex()

def digest(*parts):
 return hashlib.sha256('\x00'.join(parts).encode()).digest()[:16].hex()

def public_target(raw,owner,registration,hub_id,hub_config):
 expected=raw['expected_target']
 raw_identity='\x00'.join((expected['hub_id'],expected['owner_host_id'],expected['owner_config_generation'],expected['workspace_id'],expected['workspace_kind'],expected['session'],expected['pane'],expected['server_incarnation'],expected['target_generation']))
 return {
  'hub_id':hub_id,
  'owner_host_id':owner,
  'owner_config_generation':digest('owner-config',hub_id,hub_config,owner,registration,expected['owner_config_generation']),
  'workspace_id':owner+'\x1f'+expected['workspace_id'],
  'workspace_kind':expected['workspace_kind'],
  'session':expected['session'],
  'pane':expected['pane'],
  'server_incarnation':expected['server_incarnation'],
  'target_generation':digest('target',hub_id,hub_config,owner,registration,raw_identity),
 }

def assert_private_target(row,raw,facts,durable_created,registration,hub_id,hub_config,label):
 raw_expected=raw['expected_target']
 raw_checks={
  'owner_host_id':raw['owner_host_id'],
  'session':facts['session'],
  'pane':facts['pane'],
  'server_incarnation':'pid='+facts['pid'],
  'target_generation':raw_target_generation(raw,facts,durable_created),
 }
 for key,want in raw_checks.items():
  if raw_expected.get(key)!=want: raise RuntimeError(f'{label}: owner catalog is not the marked private terminal: {key}={raw_expected.get(key)!r}, want {want!r}')
 expected=public_target(raw,row['owner_host_id'],registration,hub_id,hub_config)
 if row['expected_target']!=expected: raise RuntimeError(f'{label}: public catalog identity does not map from the verified private owner target')

local_facts=tmux_facts(local_tmux)
remote_facts=tmux_facts(remote_tmux)
local_socket_guard=f'{os.stat(local_socket).st_dev}:{os.stat(local_socket).st_ino}'
remote_socket_guard=remote_socket_identity()
local_created=shell_created((root/'state/sidecar/projects/project/shells.json').read_text(),local_socket)
remote_created=shell_created(remote_file(f'{remote_root}/state/sidecar/projects/project/shells.json'),remote_socket)
if local_facts['geometry']!='80x24' or remote_facts['geometry']!='80x24': raise RuntimeError('private proof geometry is not the expected initial 80x24')
if local_tmux('show-options','-qv','-t','sidecar-sh-project-1','@sidecar-owner') or owner_value_remote(): raise RuntimeError('private proof terminal already has an owner')

def safe_kill_remote(required=False):
 try:
  current_socket=remote_socket_identity(); current=tmux_facts(remote_tmux)
 except Exception:
  if required: raise
  return False
 if current_socket!=remote_socket_guard or current['pid']!=remote_facts['pid'] or current['session_created']!=remote_facts['session_created']:
  raise RuntimeError('refusing to kill changed remote tmux server identity')
 remote_tmux('kill-server')
 return True

def cleanup():
 try: config.write_bytes(original_config)
 except OSError: pass
 for service in services:
  if service.p.poll() is not None: continue
  try: service.p.stdin.close()
  except (BrokenPipeError,ValueError,OSError): pass
  try: service.p.wait(timeout=2)
  except subprocess.TimeoutExpired:
   service.p.terminate()
   try: service.p.wait(timeout=2)
   except subprocess.TimeoutExpired: service.p.kill()
 try: safe_kill_remote()
 except Exception: pass
atexit.register(cleanup)

snap=catalog(); hosts={h['id']:h for h in snap['hosts']}
if hosts['legacy']['state']!='unsupported' or any(r['owner_host_id']=='legacy' for s in snap['sections'] for r in s['rows']):raise RuntimeError('legacy owner was not refused')
rows=[r for s in snap['sections'] for r in s['rows'] if r.get('attachment_ready')]
local=next(r for r in rows if r['owner_host_id'].startswith('local:')); remote=next(r for r in rows if r['owner_host_id']=='remote')
for row in (local,remote):
 if row['expected_target']['session']!='sidecar-sh-project-1' or row['expected_target']['pane']!='%0':raise RuntimeError('duplicate target setup drifted')
if local['target']==remote['target'] or local['expected_target']==remote['expected_target']:raise RuntimeError('owner scoping collapsed duplicate terminals')
local_raw=ready_private_row(owner_stdio_catalog([str(binary),'-config',str(config),'mobile','serve','--stdio','--owner-only'],env))
remote_owner_command=' '.join(shlex.quote(x) for x in [f'{remote_root}/run-sidecar','-config',f'{remote_root}/config/config.json','mobile','serve','--stdio','--owner-only'])
remote_raw=ready_private_row(owner_stdio_catalog(['ssh',remote_alias,remote_owner_command]))
hub_id=snap['hub_id']; hub_config=hashlib.sha256(original_config).digest()[:16].hex()
local_registration=digest('local-mobile-owner',os.uname().nodename,str(config))
remote_registration=digest('remote',remote_alias,f'{remote_root}/run-sidecar',f'{remote_root}/config/config.json')
assert_private_target(local,local_raw,local_facts,local_created,local_registration,hub_id,hub_config,'local')
assert_private_target(remote,remote_raw,remote_facts,remote_created,remote_registration,hub_id,hub_config,'remote')

svc=Service('retarget'); hello=svc.request('hello'); resolved=svc.request('resolve',target=remote['target'],expected_target=remote['expected_target']); opened=svc.request('open',target_handle=resolved['target']['handle'],attachment_id='router-adversarial')
f=svc.frame(); a=opened['attachment_handle']; op=1
svc.request('control',attachment_handle=a,operation_sequence=op,last_reset_generation=f['reset_generation'],last_output_sequence=f['output_sequence'],columns=f['geometry']['columns'],rows=f['geometry']['rows'])
op+=1; token=b'REMOTE-ROUTED-ONLY-20260908'
svc.request('input',attachment_handle=a,operation_sequence=op,last_reset_generation=svc.frames[-1]['reset_generation'],last_output_sequence=svc.frames[-1]['output_sequence'],data_base64=base64.b64encode(b"printf '"+token+b"\\n'\r").decode())
deadline=time.monotonic()+8
while time.monotonic()<deadline:
 m=svc.receive()
 if m and m['type']=='frame' and token in base64.b64decode(m['render_vt_base64']):break
else:raise RuntimeError('remote marker absent')
if token.decode() in local_tmux('capture-pane','-p','-t','%0'):raise RuntimeError('remote input fell back to local duplicate')

op+=1; latest=svc.frames[-1]
resized=svc.request('resize',attachment_handle=a,operation_sequence=op,last_reset_generation=latest['reset_generation'],last_output_sequence=latest['output_sequence'],columns=70,rows=20)
while True:
 rf=svc.frame()
 if rf['reset_generation']==resized['reset_generation']:break
if rf['geometry']!={'columns':70,'rows':20}:raise RuntimeError(f'remote accepted geometry mismatch {rf["geometry"]}')
if local_tmux('display-message','-p','-t','%0','#{pane_width}x#{pane_height}')!='80x24':raise RuntimeError('remote resize mutated local duplicate')
remote_geometry=remote_tmux('display-message','-p','-t','%0','#{pane_width}x#{pane_height}')
if remote_geometry!='70x20':raise RuntimeError(f'remote pane did not accept geometry: {remote_geometry!r}')

op+=1
loop=b"i=0; while [ $i -lt 80 ]; do printf 'ROUTER-OUT-%03d\\n' \"$i\"; i=$((i+1)); sleep 0.08; done &\r"
svc.request('input',attachment_handle=a,operation_sequence=op,last_reset_generation=rf['reset_generation'],last_output_sequence=rf['output_sequence'],data_base64=base64.b64encode(loop).decode())
lat=[]
for i in range(30):
 op+=1; latest=svc.frames[-1]; started=time.monotonic()
 if i%2==0: svc.request('input',attachment_handle=a,operation_sequence=op,last_reset_generation=latest['reset_generation'],last_output_sequence=latest['output_sequence'],data_base64=base64.b64encode(b' ').decode())
 else: svc.request('heartbeat',attachment_handle=a,operation_sequence=op,last_reset_generation=latest['reset_generation'],last_output_sequence=latest['output_sequence'])
 lat.append((time.monotonic()-started)*1000)
deadline=time.monotonic()+10
while time.monotonic()<deadline:
 m=svc.receive()
 if m and m['type']=='frame' and b'ROUTER-OUT-079' in base64.b64decode(m['render_vt_base64']):break
else:raise RuntimeError('newest output absent')
post_resize=[x['output_sequence'] for x in svc.frames if x['reset_generation']==rf['reset_generation']]
if any(b<=a for a,b in zip(post_resize,post_resize[1:])):raise RuntimeError('frame sequence not increasing')

changed=json.loads(original_config); changed['hosts']['list'][0]['target']='retargeted.invalid'
try:
 config.write_text(json.dumps(changed,separators=(',',':'))+'\n')
 op+=1; stale_release=svc.request('release',expect_error=True,attachment_handle=a,operation_sequence=op,last_reset_generation=svc.frames[-1]['reset_generation'],last_output_sequence=svc.frames[-1]['output_sequence'])
 try: exit_code=svc.p.wait(timeout=8)
 except subprocess.TimeoutExpired: raise RuntimeError('retargeted broker did not close')
finally:
 config.write_bytes(original_config)
wait_owner_empty()
if owner_value_remote():raise RuntimeError('retarget cleanup failed')

loss=Service('owner-loss'); loss_hello=loss.request('hello'); loss_resolved=loss.request('resolve',target=remote['target'],expected_target=remote['expected_target']); loss_open=loss.request('open',target_handle=loss_resolved['target']['handle'],attachment_id='router-owner-loss'); lf=loss.frame(); la=loss_open['attachment_handle']
loss.request('control',attachment_handle=la,operation_sequence=1,last_reset_generation=lf['reset_generation'],last_output_sequence=lf['output_sequence'],columns=lf['geometry']['columns'],rows=lf['geometry']['rows'])
assert_private_target(remote,remote_raw,tmux_facts(remote_tmux),remote_created,remote_registration,hub_id,hub_config,'remote before owner-loss mutation')
safe_kill_remote(required=True)
closed=False; deadline=time.monotonic()+12
while time.monotonic()<deadline:
 m=loss.receive(timeout=max(.1,deadline-time.monotonic()))
 if m is None:closed=True;break
 if m['type']=='error':
  try: loss.p.wait(timeout=8); closed=True
  except subprocess.TimeoutExpired: pass
  break
if not closed:raise RuntimeError('owner server loss did not close public stream')

refuse=Service('post-loss'); refuse.request('hello'); refused=refuse.request('resolve',expect_error=True,target=remote['target'],expected_target=remote['expected_target'])
refuse.close_normal()
if not local_tmux('has-session','-t','sidecar-sh-project-1')=='':raise RuntimeError('local duplicate was lost')
local_capture=local_tmux('capture-pane','-p','-t','%0')
if token.decode() in local_capture:raise RuntimeError('remote loss fell back to local duplicate')

summary={'schema':'sidecar.mobile.router-adversarial-proof.v0','hosts':[(h['id'],h['state']) for h in snap['hosts']],
 'duplicate_identity':{'session':'sidecar-sh-project-1','pane':'%0','public_selectors_distinct':True},'selected_owner':'remote','remote_geometry_after_resize':{'columns':70,'rows':20},'local_geometry_after_remote_resize':{'columns':80,'rows':24},
 'remote_input_visible':True,'local_duplicate_unchanged':True,'legacy_owner_state':'unsupported','operations_under_output':30,'request_to_correlated_ack_latency_ms':{'median':round(statistics.median(lat),3),'p95':round(sorted(lat)[math.ceil(len(lat)*.95)-1],3),'max':round(max(lat),3)},
 'post_resize_frames':len(post_resize),'newest_output_visible':'ROUTER-OUT-079','retarget_release_result':stale_release.get('error',{}).get('code',stale_release['type']),'retarget_process_exit':exit_code,'owner_after_retarget':'empty','owner_loss_closed_stream':closed,'post_loss_resolve':refused.get('error',{}).get('code'),'local_survived_owner_loss':True,'records':len(records)}
(root/'adversarial-transcript.jsonl').write_text(''.join(json.dumps(r,separators=(',',':'),sort_keys=True)+'\n' for r in records))
(root/'adversarial-result.json').write_text(json.dumps(summary,indent=2,sort_keys=True)+'\n')
print(json.dumps(summary,indent=2,sort_keys=True))
