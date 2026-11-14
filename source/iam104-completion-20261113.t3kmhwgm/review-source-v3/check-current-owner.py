import hashlib,json,os,pathlib,subprocess,tempfile,time
root=pathlib.Path(__file__).parent
repo=pathlib.Path((root/'repository-path').read_text().strip())
paths=json.loads((root/'product-owner-paths.json').read_text())
before={x:hashlib.sha256((repo/x).read_bytes()).hexdigest() for x in paths}
record=root/('owner-check-'+str(time.time_ns()))
record.mkdir(mode=0o700)
env=os.environ|{'GOFLAGS':'-p=1 -mod=readonly','GOTOOLCHAIN':'local','GOMAXPROCS':'2','GOMEMLIMIT':'512MiB','GOPROXY':'off','GOSUMDB':'off','TMPDIR':str(pathlib.Path(tempfile.gettempdir()).resolve())}
commands={
'unit':['go','-C','services/control','test','-count=1','-json','-timeout=60s','./internal/modules/identity','./internal/modules/sso','-run','^(TestSourceSessionDeadlineBoundary|TestIssueSessionUntilRejectsInvalidAuthorityBeforeStorage|TestSAMLCompletionProtectedInputDiagnostics|TestSAMLCompletionRequiresActualOwner|TestSAMLCompletionVersionDomain)$'],
'compile-only':['go','-C','services/control','test','-tags','repository_integration','-overlay='+str(root/'overlay.json'),'-modfile='+str(root/'control.mod'),'-run','^$','./internal/modules/sso']}
for name,cmd in commands.items():
 with (record/(name+'.log')).open('xb') as out:r=subprocess.run(cmd,cwd=repo,env=env|({'GOWORK':'off'} if name=='compile-only' else {}),stdout=out,stderr=subprocess.STDOUT,timeout=90)
 (record/(name+'-command.json')).write_text(json.dumps({'command':cmd,'return_code':r.returncode,'database_started':False,'compile_only':name=='compile-only'},indent=2)+'\n')
 print(name,r.returncode,flush=True)
 if r.returncode:raise SystemExit(r.returncode)
rows=[json.loads(x) for x in (record/'unit.log').read_text().splitlines()]
passed=[x['Test'] for x in rows if x.get('Action')=='pass' and x.get('Test')]
assert len([x for x in passed if '/' not in x])==5
assert not any(x.get('Action') in ('fail','skip') for x in rows)
assert before=={x:hashlib.sha256((repo/x).read_bytes()).hexdigest() for x in paths},'OWNER_CHANGED_DURING_CHECK'
(record/'source-hashes.json').write_text(json.dumps(before,indent=2)+'\n')
(record/'result.json').write_text(json.dumps({'passed_tests':passed,'roots':5,'leaves':len([x for x in passed if '/' in x]),'database_cases_executed':0,'base':subprocess.check_output(['git','rev-parse','HEAD'],cwd=repo,text=True).strip()},indent=2)+'\n')
(root/'latest-owner-check').write_text(str(record)+'\n')
print('NO_DATABASE_OWNER_CHECKS_PASSED',record,flush=True)
