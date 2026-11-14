import pathlib,os,subprocess,json,tempfile,hashlib,shutil
p=pathlib.Path(__file__).parent;proto=p/'prototype';stage=pathlib.Path(tempfile.mkdtemp(prefix='native-',dir=p));stage.chmod(0o700);(p/'active-native-path').write_text(str(stage)+'\n')
if shutil.disk_usage(p).free<15*1024**3:raise RuntimeError('disk resource refusal')
env=os.environ|{'GOWORK':'off','GOTOOLCHAIN':'local','GOFLAGS':'-p=2 -mod=readonly','GOMAXPROCS':'2','GOPROXY':'off','GOSUMDB':'off'}
for args,name in [(['gofmt','-w',*map(str,proto.glob('*.go'))],'format.log'),(['go','test','-race','-count=1','-json','./...'],'race.jsonl')]:
 with (stage/name).open('w') as out:r=subprocess.run(args,cwd=proto,env=env,stdout=out,stderr=subprocess.STDOUT,timeout=180)
 if r.returncode:raise RuntimeError(name+' failed: '+str(stage/name))
 if name=='format.log':
  shutil.copytree(proto,stage/'source');(stage/'source-hashes.json').write_text(json.dumps({f.name:hashlib.sha256(f.read_bytes()).hexdigest() for f in proto.iterdir() if f.is_file()},indent=2)+'\n')
rows=[json.loads(x) for x in (stage/'race.jsonl').read_text().splitlines()];roots=[x['Test'] for x in rows if x.get('Action')=='pass' and x.get('Test') and '/' not in x['Test']];(stage/'success.json').write_text(json.dumps({'passed_roots':roots,'race':True},indent=2)+'\n');print(json.dumps({'stage':str(stage),'passed_roots':roots}),flush=True)
