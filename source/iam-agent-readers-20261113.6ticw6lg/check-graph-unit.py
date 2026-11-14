import hashlib,json,os,pathlib,subprocess,tempfile,time,re,shutil
p=pathlib.Path(__file__).parent;repo=pathlib.Path((p/'repository-path').read_text().strip())
sample=subprocess.check_output(['vm_stat'],text=True)
page=int(re.search(r'page size of (\d+) bytes',sample).group(1))
free=int(re.search(r'Pages free:\s+(\d+)',sample).group(1))*page
disk=shutil.disk_usage(p).free
preflight={'free_bytes':free,'minimum_free_bytes':2*1024**3,'disk_free_bytes':disk,'minimum_disk_free_bytes':15*1024**3,'observed_at':time.time()}
(p/('resource-preflight-'+str(time.time_ns())+'.json')).write_text(json.dumps(preflight,indent=2)+'\n')
if free<preflight['minimum_free_bytes'] or disk<preflight['minimum_disk_free_bytes']:
 print('GRAPH_UNIT_RESOURCE_REFUSED',json.dumps(preflight),flush=True);raise SystemExit(78)
paths=['services/control/internal/modules/identity/http/agent_path.go','services/control/internal/modules/identity/http/agent_published_readers.go','services/control/internal/modules/identity/http/agent_published_readers_test.go']
source={x:hashlib.sha256((repo/x).read_bytes()).hexdigest() for x in paths}
record=p/('unit-'+str(time.time_ns()));record.mkdir(mode=0o700)
(record/'source-hashes.json').write_text(json.dumps(source,indent=2)+'\n')
(record/'producer-hashes.json').write_bytes((p/'original-owner-hashes.json').read_bytes())
env=os.environ|{'GOTOOLCHAIN':'local','GOMAXPROCS':'2','GOMEMLIMIT':'512MiB','GOFLAGS':'-p=1 -mod=readonly','GOPROXY':'off','GOSUMDB':'off','TMPDIR':str(pathlib.Path(tempfile.gettempdir()).resolve())}
command=['go','-C','services/control','test','-overlay='+str(p/'overlay.json'),'-count=1','-json','-timeout=45s','./internal/modules/identity/http','-run','^(TestAgentGatewayPublishedReaderGraph|TestAgentGatewayPublishedReaderFailureCleanup|TestAgentGatewayPublishedReaderRequiresTrustedOwner)$']
with (record/'unit.log').open('xb') as out:r=subprocess.run(command,cwd=repo,env=env,stdout=out,stderr=subprocess.STDOUT,timeout=120)
(record/'command.json').write_text(json.dumps({'command':command,'return_code':r.returncode,'database_started':False,'producer_overlay':str(p/'overlay.json'),'input_base':subprocess.check_output(['git','rev-parse','HEAD'],cwd=repo,text=True).strip()},indent=2)+'\n')
print('graph-unit',r.returncode,record,flush=True)
if r.returncode:raise SystemExit(r.returncode)
rows=[json.loads(x) for x in (record/'unit.log').read_text().splitlines()];passed=[x['Test'] for x in rows if x.get('Action')=='pass' and x.get('Test')]
assert len([x for x in passed if '/' not in x])==3 and not any(x.get('Action') in ('fail','skip') for x in rows)
assert source=={x:hashlib.sha256((repo/x).read_bytes()).hexdigest() for x in paths}
(record/'result.json').write_text(json.dumps({'passed_tests':passed,'roots':3,'subtests':len([x for x in passed if '/' in x]),'database_cases_executed':0,'installed_gateway':False,'runtime_registration':False},indent=2)+'\n')
(p/'latest-check').write_text(str(record)+'\n');print('AGENT_READER_NO_DATABASE_GRAPH_TESTS_PASSED',flush=True)
