from pathlib import Path
import json,os,re,subprocess,sys,time,uuid
name=sys.argv[1];root=Path(sys.argv[2]);assert re.fullmatch(r'ownsolid-e2e-[a-z0-9-]+',name);assert root.name=='raw' and root.parent.name=='fleet-kpis' and root.parent.parent.name.startswith('lane-arc107-handoff.3ae6bfeb.') and root.is_dir()
os.umask(0o077)
commands={'outer-state':['docker','inspect','--format','{{json .State}}',name],'outer-daemon':['docker','logs','--tail','200',name],'inner-containers':['docker','exec',name,'docker','ps','-a','--format','{{json .}}'],'egress':['docker','exec',name,'cat','/var/log/ownsolid-egress.jsonl'],'routes-v4':['docker','exec',name,'ip','route','show'],'routes-v6':['docker','exec',name,'ip','-6','route','show'],'interfaces':['docker','exec',name,'ip','addr','show']}
for container in ['ownsolid-control-1','ownsolid-postgres-1','ownsolid-age-gateway-a','ownsolid-age-gateway-b']:commands[container]=['docker','exec',name,'docker','logs',container]
query="SELECT json_build_object('server_start',pg_postmaster_start_time(),'stats_reset',stats_reset,'database',datname,'track_counts',current_setting('track_counts')) FROM pg_stat_database WHERE datname=current_database()"
commands['database-statistics-state']=['docker','exec',name,'docker','compose','--env-file','/srv/ownsolid/compose.env','-f','/srv/ownsolid/compose.yaml','exec','--no-TTY','postgres','psql','-X','-tA','-U','ownsolid_migrator','-d','ownsolid','-c',query]
commands['database-container-state']=['docker','exec',name,'docker','inspect','--format','{{json .State}} {{.RestartCount}}','ownsolid-postgres-1']
for key,command in commands.items():
 try:
  r=subprocess.run(command,capture_output=True,timeout=2);state={'at':time.time(),'outer':name,'code':r.returncode,'stdout_bytes':len(r.stdout),'stderr':r.stderr.decode(errors='replace')[-2000:]}
  if r.returncode==0:
   if key=='database-statistics-state':
    observation={'at':time.time(),'outer':name,'state':json.loads(r.stdout)}
    with(root/'database-observations.jsonl').open('a')as history:history.write(json.dumps(observation)+'\n')
   p=root/(key+'.log');tmp=root/(key+'.'+uuid.uuid4().hex+'.tmp');tmp.write_bytes(r.stdout+r.stderr if key in ['outer-daemon','ownsolid-control-1','ownsolid-postgres-1','ownsolid-age-gateway-a','ownsolid-age-gateway-b'] else r.stdout);tmp.replace(p)
 except subprocess.TimeoutExpired:state={'at':time.time(),'outer':name,'code':124}
 tmp=root/(key+'.'+uuid.uuid4().hex+'.json.tmp');tmp.write_text(json.dumps(state)+'\n');tmp.replace(root/(key+'-last-read.json'))
