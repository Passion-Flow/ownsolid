"""Owner-only native samples with precise PID lifetime and transitive ancestry."""
import ctypes,errno,os,time

class SampleGap(RuntimeError):pass
class Bsd(ctypes.Structure):
 _fields_=[(n,ctypes.c_uint32) for n in ['flags','status','xstatus','pid','ppid','uid','gid','ruid','rgid','svuid','svgid','reserved']]+[('comm',ctypes.c_char*16),('name',ctypes.c_char*32)]+[(n,ctypes.c_uint32) for n in ['nfiles','pgid','jobc','tdev','tpgid']]+[('nice',ctypes.c_int32),('start_sec',ctypes.c_uint64),('start_usec',ctypes.c_uint64)]
class Task(ctypes.Structure):
 _fields_=[(n,ctypes.c_uint64) for n in ['virtual','resident','user','system','threads_user','threads_system']]+[(n,ctypes.c_int32) for n in ['policy','faults','pageins','cow','sent','received','mach','unix','csw','threads','running','priority']]
class All(ctypes.Structure):_fields_=[('bsd',Bsd),('task',Task)]
assert ctypes.sizeof(Bsd)==136 and ctypes.sizeof(Task)==96 and ctypes.sizeof(All)==232
lib=ctypes.CDLL('/usr/lib/libproc.dylib',use_errno=True)
lib.proc_pidinfo.argtypes=[ctypes.c_int,ctypes.c_int,ctypes.c_uint64,ctypes.c_void_p,ctypes.c_int];lib.proc_pidinfo.restype=ctypes.c_int
lib.proc_listchildpids.argtypes=[ctypes.c_int,ctypes.c_void_p,ctypes.c_int];lib.proc_listchildpids.restype=ctypes.c_int

def info(pid,allow_exit=False):
 value=All();ctypes.set_errno(0);count=lib.proc_pidinfo(pid,2,0,ctypes.byref(value),ctypes.sizeof(value));err=ctypes.get_errno()
 if count!=ctypes.sizeof(value):
  # A kernel-confirmed zombie has no live resident task; this is an exit event,
  # not permission failure or an unexplained missing RSS sample.
  bsd=Bsd();ctypes.set_errno(0);bsd_count=lib.proc_pidinfo(pid,3,0,ctypes.byref(bsd),ctypes.sizeof(bsd))
  if bsd_count==ctypes.sizeof(bsd) and bsd.status==5 and bsd.pid==pid and bsd.uid==os.getuid() and bsd.ruid==os.getuid():
   return {'pid':bsd.pid,'ppid':bsd.ppid,'pgid':bsd.pgid,'uid':bsd.uid,'startTime':[bsd.start_sec,bsd.start_usec],'rssBytes':0,'status':5,'kernelZombie':True}
  if allow_exit:
   try:os.kill(pid,0)
   except ProcessLookupError:return None
   except PermissionError:pass
  raise SampleGap('Cannot read exact owned task sample: pid=%d bytes=%d errno=%d'%(pid,count,err))
 b=value.bsd
 if b.pid!=pid or b.uid!=os.getuid() or b.ruid!=os.getuid():raise SampleGap('Owned PID or UID mismatch')
 return {'pid':b.pid,'ppid':b.ppid,'pgid':b.pgid,'uid':b.uid,'startTime':[b.start_sec,b.start_usec],'rssBytes':value.task.resident,'status':b.status}

def children(pid):
 # Bound one ownership listing; a full buffer is an error, never silent truncation.
 values=(ctypes.c_int*4096)();ctypes.set_errno(0);count=lib.proc_listchildpids(pid,values,ctypes.sizeof(values));err=ctypes.get_errno()
 if count<0 or count>=len(values) or (count==0 and err):raise SampleGap('Owned child listing failed/truncated: pid=%d count=%d errno=%d'%(pid,count,err))
 return [values[i] for i in range(count) if values[i]>0]

class OwnerSampler:
 def __init__(self,guard_pid,runner_pid):
  self.guard=guard_pid;self.runner=runner_pid;self.known={};self.samples=0;self.cpu_seconds=0.;self.wall_seconds=0.;self.cross_edges=set()
  guard=info(guard_pid);runner=info(runner_pid);cursor=runner;self.lineage=[]
  for _ in range(64):
   self.lineage.append({'pid':cursor['pid'],'ppid':cursor['ppid'],'startTime':cursor['startTime']})
   if cursor['pid']==guard_pid:break
   if cursor['ppid']<=1 and cursor['ppid']!=guard_pid:raise SampleGap('Runner ancestry does not reach admitted guard')
   cursor=info(cursor['ppid'])
  else:raise SampleGap('Runner ancestry exceeded bound')
  self.guard_start=guard['startTime'];self.runner_start=runner['startTime']
 def snapshot(self):
  started=time.monotonic();cpu=time.process_time();rows={};edges=[];retired=[];reused=[];queue=[self.guard]
  for pid,start in list(self.known.items()):
   if pid==self.guard:continue
   row=info(pid,allow_exit=True)
   if row is None:retired.append({'pid':pid,'startTime':start,'kernelExitConfirmed':True});continue
   if row['startTime']!=start:reused.append({'pid':pid,'oldStart':start,'newStart':row['startTime'],'excluded':True});continue
   queue.append(pid)
  while queue:
   pid=queue.pop(0)
   if pid in rows:continue
   row=info(pid)
   if pid in self.known and row['startTime']!=self.known[pid]:raise SampleGap('PID lifetime changed after ownership observation')
   if pid==self.guard and row['startTime']!=self.guard_start:raise SampleGap('Guard PID lifetime changed')
   if pid==self.runner and row['startTime']!=self.runner_start:raise SampleGap('Runner PID lifetime changed')
   rows[pid]=row;self.known[pid]=row['startTime']
   for child in children(pid):
    child_row=info(child)
    if child_row['ppid']!=pid:raise SampleGap('Child ownership changed during enumeration')
    previous=self.known.get(child)
    if previous is not None and previous!=child_row['startTime']:reused.append({'pid':child,'oldStart':previous,'newStart':child_row['startTime'],'newAncestryVerified':True})
    self.known[child]=child_row['startTime'];edges.append({'parent':pid,'parentStart':row['startTime'],'child':child,'childStart':child_row['startTime']})
    if child_row['pgid']!=row['pgid'] and child!=self.runner:self.cross_edges.add((pid,child,row['pgid'],child_row['pgid']))
    queue.append(child)
   after=info(pid)
   if after['startTime']!=row['startTime']:raise SampleGap('Parent lifetime changed while enumerating children')
  if self.guard not in rows or self.runner not in rows:raise SampleGap('Missing guard/runner sample')
  self.samples+=1;spent_cpu=time.process_time()-cpu;spent_wall=time.monotonic()-started;self.cpu_seconds+=spent_cpu;self.wall_seconds+=spent_wall
  return {'sample':self.samples,'sampleMonotonic':time.clock_gettime(time.CLOCK_MONOTONIC),'rows':list(rows.values()),'edges':edges,'retired':retired,'pidReuse':reused,'completeOwnedSnapshot':True,'sumRssBytes':sum(r['rssBytes'] for r in rows.values()),'samplerRssBytes':rows[self.runner]['rssBytes'],'samplerCpuSeconds':spent_cpu,'samplerWallSeconds':spent_wall,'crossPgidEdges':len(self.cross_edges)}
