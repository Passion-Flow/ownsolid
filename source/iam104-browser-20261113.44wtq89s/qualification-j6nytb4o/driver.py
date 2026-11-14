import hashlib,json,os,pathlib,shutil,signal,subprocess,tempfile
os.umask(0o077)
p=pathlib.Path(__file__).resolve().parent
repo=pathlib.Path((p/'repository-path').read_text().strip())
if shutil.disk_usage(p).free<15*1024**3:raise SystemExit('private browser disk budget unavailable')
def stop(signum,frame):raise KeyboardInterrupt
signal.signal(signal.SIGTERM,stop)
stage=pathlib.Path(tempfile.mkdtemp(prefix='qualification-',dir=p));(p/'active-qualification').write_text(str(stage)+'\n')
env=os.environ|{'GOWORK':'off','GOTOOLCHAIN':'local','GOMAXPROCS':'2','GOFLAGS':'-p=2 -mod=readonly','GOPROXY':'off','GOSUMDB':'off','TMPDIR':str(pathlib.Path(tempfile.gettempdir()).resolve()),'PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD':'1'}
def run(args,name,environment=env,timeout=180):
 with (stage/(name+'.log')).open('wb') as out:result=subprocess.run(args,cwd=repo,env=environment,stdout=out,stderr=subprocess.STDOUT,timeout=timeout)
 print(name,result.returncode,flush=True)
 if result.returncode:raise RuntimeError(name+' failed; private evidence '+str(stage))
 return (stage/(name+'.log')).read_text()
run(['gofmt','-w',*map(str,(p/'prototype').rglob('*.go'))],'format')
run([json.loads((p/'runtime-base.json').read_text())['node_executable'],'--check',str(p/'probe.mjs')],'javascript-syntax')
shutil.copytree(p/'prototype',stage/'source');shutil.copy2(p/'probe.mjs',stage/'source'/'probe.mjs');shutil.copy2(__file__,stage/'driver.py')
hashes={str(f.relative_to(stage/'source')):hashlib.sha256(f.read_bytes()).hexdigest() for f in (stage/'source').rglob('*') if f.is_file()};(stage/'source-hashes.json').write_text(json.dumps(hashes,indent=2)+'\n');(stage/'input-sha').write_bytes(subprocess.check_output(['git','rev-parse','HEAD'],cwd=repo))
runtime=json.loads((p/'runtime-base.json').read_text())|{'script':str(p/'probe.mjs'),'directory':str(stage)};runtimeFile=stage/'runtime.json';runtimeFile.write_text(json.dumps(runtime,indent=2)+'\n')
raw=run(['go','-C',str(p/'prototype'),'test','-race','-count=1','-json','-timeout=120s','./...'],'native-browser-tests',environment=env|{'IAM_SAML_BROWSER_RUNTIME_FILE':str(runtimeFile)},timeout=150)
rows=[json.loads(line) for line in raw.splitlines()];assert [row['Test'] for row in rows if row.get('Action')=='pass' and row.get('Test')]==['TestActualCrossSiteCookieAndFirstPartyContinuation'];assert not any(row.get('Action') in ['skip','fail'] for row in rows)
browser=json.loads((stage/'browser-results.json').read_text());observations=json.loads((stage/'server-observations.json').read_text());assert browser['unpinned_certificate_refused'] and len(browser['cases'])==3 and observations['authentication_grants_created']==0
credits=stage/'browser-credits.html';assert credits.stat().st_size>10000
(stage/'success.json').write_text(json.dumps({'runtime_browser':browser['browser'],'playwright':browser['playwright'],'transport_cases':3,'unpinned_certificate_control':True,'race':True,'product_authentication_qualified':False,'native_mac_not_egress_isolated':True,'credits_sha256':hashlib.sha256(credits.read_bytes()).hexdigest()},indent=2)+'\n');print('SAML_BROWSER_TRANSPORT_PROBE_PASSED',stage,flush=True)
