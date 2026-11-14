import hashlib,json,os,pathlib,re,shutil,signal,subprocess,tempfile,time
p=pathlib.Path(__file__).parent;repo=pathlib.Path((p/'repository-path').read_text().strip())
record=p/('http-unit-'+str(time.time_ns()));record.mkdir(mode=0o700)
sample=subprocess.check_output(['vm_stat'],text=True);page=int(re.search(r'page size of (\d+) bytes',sample).group(1));free=int(re.search(r'Pages free:\s+(\d+)',sample).group(1))*page;disk=shutil.disk_usage(p).free
preflight={'free_bytes':free,'minimum_free_bytes':2*1024**3,'disk_free_bytes':disk,'minimum_disk_free_bytes':15*1024**3,'database_started':False}
(record/'resource-preflight.json').write_text(json.dumps(preflight,indent=2)+'\n')
if free<2*1024**3 or disk<15*1024**3:print('SAML_HTTP_UNIT_RESOURCE_REFUSED',record,flush=True);raise SystemExit(78)
paths=json.loads((p/'http-product-review-paths.json').read_text());hashes={name:hashlib.sha256((repo/name).read_bytes()).hexdigest() for name in paths}
(record/'source-hashes.json').write_text(json.dumps(hashes,indent=2)+'\n')
env=os.environ|{'GOTOOLCHAIN':'local','GOMAXPROCS':'2','GOMEMLIMIT':'512MiB','GOFLAGS':'-p=1 -mod=readonly','GOPROXY':'off','GOSUMDB':'off','TMPDIR':str(pathlib.Path(tempfile.gettempdir()).resolve())}
roots=['TestSourceSessionDeadlineBoundary','TestIssueSessionUntilRejectsInvalidAuthorityBeforeStorage','TestSAMLCompletionProtectedInputDiagnostics','TestSAMLCompletionRequiresActualOwner','TestSAMLCompletionVersionDomain','TestSAMLCompletionHTTPRoute','TestSAMLCompletionHTTPBrowserInput','TestSAMLCompletionHTTPInputRefusals','TestSAMLCompletionHTTPRequiresSameOwner','TestSAMLCompletionKeepsOrdinaryHTTPBoundary']
command=['go','-C','services/control','test','-count=1','-json','-timeout=45s','./internal/modules/identity','./internal/modules/sso','./internal/modules/sso/http','./internal/modules/identity/http','-run','^('+'|'.join(roots)+')$']
with (record/'unit.log').open('xb') as output:result=subprocess.run(command,cwd=repo,env=env,stdout=output,stderr=subprocess.STDOUT,timeout=90)
(record/'command.json').write_text(json.dumps({'command':command,'return_code':result.returncode,'database_cases_executed':0,'input_base':subprocess.check_output(['git','rev-parse','HEAD'],cwd=repo,text=True).strip()},indent=2)+'\n')
print('SAML_HTTP_UNIT_EXIT',result.returncode,record,flush=True)
if result.returncode:raise SystemExit(result.returncode)
rows=[json.loads(x) for x in (record/'unit.log').read_text().splitlines()];passed=[x['Test'] for x in rows if x.get('Action')=='pass' and x.get('Test')]
assert set(x for x in passed if '/' not in x)==set(roots) and not any(x.get('Action') in ('fail','skip') for x in rows)
assert hashes=={name:hashlib.sha256((repo/name).read_bytes()).hexdigest() for name in paths}
(record/'result.json').write_text(json.dumps({'roots':len(roots),'subtests':len([x for x in passed if '/' in x]),'passed_tests':passed,'database_cases_executed':0,'provider_started':False,'gateway_graph_included':False},indent=2)+'\n')
(p/'latest-http-unit-check').write_text(str(record)+'\n');print('SAML_HTTP_NO_DATABASE_UNIT_PASSED',flush=True)
