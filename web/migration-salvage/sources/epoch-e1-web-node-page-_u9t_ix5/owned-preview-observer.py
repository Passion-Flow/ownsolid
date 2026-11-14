"""Bounded identities and HTTP status for this job's own preview only; no RSS."""
import ctypes, datetime, errno, hashlib, http.client, json, os, re, subprocess, time

class Bsd(ctypes.Structure):
 _fields_=[(n,ctypes.c_uint32) for n in ['flags','status','xstatus','pid','ppid','uid','gid','ruid','rgid','svuid','svgid','reserved']]+[('comm',ctypes.c_char*16),('name',ctypes.c_char*32)]+[(n,ctypes.c_uint32) for n in ['nfiles','pgid','jobc','tdev','tpgid']]+[('nice',ctypes.c_int32),('start_sec',ctypes.c_uint64),('start_usec',ctypes.c_uint64)]
assert ctypes.sizeof(Bsd)==136
lib=ctypes.CDLL('/usr/lib/libproc.dylib',use_errno=True)
lib.proc_pidinfo.argtypes=[ctypes.c_int,ctypes.c_int,ctypes.c_uint64,ctypes.c_void_p,ctypes.c_int];lib.proc_pidinfo.restype=ctypes.c_int
lib.proc_listchildpids.argtypes=[ctypes.c_int,ctypes.c_void_p,ctypes.c_int];lib.proc_listchildpids.restype=ctypes.c_int
def identity(pid):
 v=Bsd();ctypes.set_errno(0);n=lib.proc_pidinfo(pid,3,0,ctypes.byref(v),ctypes.sizeof(v));e=ctypes.get_errno()
 if n!=ctypes.sizeof(v):raise RuntimeError('BSD identity unavailable pid=%d bytes=%d errno=%d'%(pid,n,e))
 if v.pid!=pid or v.uid!=os.getuid() or v.ruid!=os.getuid():raise RuntimeError('Owned identity mismatch')
 return {'pid':v.pid,'ppid':v.ppid,'pgid':v.pgid,'uid':v.uid,'startTime':[v.start_sec,v.start_usec],'status':v.status}
def children(pid):
 values=(ctypes.c_int*4096)();ctypes.set_errno(0);n=lib.proc_listchildpids(pid,values,ctypes.sizeof(values));e=ctypes.get_errno()
 if n<0 or n>=len(values) or (not n and e):raise RuntimeError('Owned child list unavailable/truncated')
 return [values[i] for i in range(n) if values[i]>0]
def utc():return datetime.datetime.now(datetime.timezone.utc).isoformat()
class Observer:
 def __init__(self,root,guard,deadline):
  self.root=root;self.guard=guard;self.deadline=deadline;self.guard_birth=identity(guard)['startTime'];self.known={};self.errors=0;self.samples=0;self.unavailable=set();self.ports=[31036]
 def event(self,kind,**fields):
  with (self.root/'owned-preview-timeline.jsonl').open('a') as out:out.write(json.dumps({'utc':utc(),'monotonic':time.clock_gettime(time.CLOCK_MONOTONIC),'event':kind,**fields})+'\n')
 def tick(self):
  self.samples+=1;rows={};queue=[self.guard];errors=[]
  # Retained lifetimes may be reparented; a PID replacement is never adopted.
  for pid,birth in list(self.known.items()):
   if pid==self.guard:continue
   try:
    row=identity(pid)
    if row['startTime']==birth:queue.append(pid)
    else:self.event('old-lifetime-replaced',pid=pid,oldStart=birth,newStart=row['startTime'])
   except RuntimeError as e:
    key=(pid,tuple(birth))
    if key not in self.unavailable:self.event('known-task-unavailable',pid=pid,startTime=birth,detail=str(e),exitClaimed=False);self.unavailable.add(key)
  while queue:
   pid=queue.pop(0)
   if pid in rows:continue
   try:
    row=identity(pid)
    if pid==self.guard and row['startTime']!=self.guard_birth:raise RuntimeError('Guard lifetime changed')
    if pid in self.known and self.known[pid]!=row['startTime']:raise RuntimeError('Known lifetime changed')
    pending=[]
    for child in children(pid):
     try:
      childrow=identity(child)
      if childrow['ppid']!=pid:raise RuntimeError('Child parent changed')
      pending.append(childrow)
     except RuntimeError as e:errors.append(str(e))
    if identity(pid)['startTime']!=row['startTime']:raise RuntimeError('Parent lifetime changed while listing children')
    rows[pid]=row;self.known[pid]=row['startTime']
    for childrow in pending:self.known[childrow['pid']]=childrow['startTime'];queue.append(childrow['pid'])
   except RuntimeError as e:errors.append(str(e))
  try:
   # Only this preflight-empty job port is queried; no global process/socket sampling.
   result=subprocess.run(['lsof','-nP',*['-iTCP:'+str(port) for port in self.ports],'-sTCP:LISTEN','-F','pfni'],capture_output=True,text=True,timeout=min(1,max(.01,self.deadline-time.clock_gettime(time.CLOCK_MONOTONIC))))
   listeners=[];entry={}
   for line in result.stdout.splitlines():
    if line.startswith('p'):entry={'pid':int(line[1:])}
    elif line.startswith('f'):entry['fd']=line[1:]
    elif line.startswith('i'):entry['socketIdentity']=line[1:]
    elif line.startswith('n'):
     row=rows.get(entry.get('pid'))
     if row is None:errors.append('Listener lacks current verified owned ancestry');continue
     after=identity(row['pid'])
     if after['startTime']!=row['startTime']:errors.append('Listener PID reused');continue
     listeners.append({**entry,'address':line[1:],'startTime':row['startTime'],'ppid':row['ppid'],'pgid':row['pgid']})
   self.event('owned-identities-and-listeners',sample=self.samples,rows=list(rows.values()),listeners=listeners,errors=errors,lsofExit=result.returncode)
   for port in self.ports:
    matched=[entry for entry in listeners if entry['address'].endswith(':'+str(port))]
    if not matched:continue
    start=time.clock_gettime(time.CLOCK_MONOTONIC);connection=http.client.HTTPConnection('localhost',port,timeout=.35)
    try:
     connection.request('GET','/',headers={'User-Agent':'ownsolid-preview-health'})
     response=connection.getresponse();self.event('owned-health-status',status=response.status,elapsedMilliseconds=(time.clock_gettime(time.CLOCK_MONOTONIC)-start)*1000,port=port,listeners=matched)
    except Exception as e:self.event('owned-health-error',errorClass=type(e).__name__,errno=getattr(e,'errno',None),elapsedMilliseconds=(time.clock_gettime(time.CLOCK_MONOTONIC)-start)*1000,listeners=listeners)
    finally:connection.close()
  except Exception as e:errors.append(type(e).__name__+':'+str(e));self.event('observation-unavailable',errorClass=type(e).__name__)
  self.errors+=len(errors)
  groups={r['pgid'] for r in rows.values() if r['pgid']!=os.getpgid(self.guard)}
  (self.root/'owned-stage-groups.json').write_text(json.dumps(sorted(groups|{os.getpgrp()}))+'\n')
  return errors
