import base64,hashlib,json,pathlib,subprocess
r=pathlib.Path('/Users/passion./.local/state/ownsolid/lane-rel');i=r/'identity-audit-contract'
previous=json.loads((i/'joint-preparation-sync-previous-sha256.json').read_text())
names=['identity-audit-contract/handoff.json','identity-audit-contract/relay/handoff.json','api-compat/handoff.json','dev102-current-handoff.json','identity-audit-contract/joint-full-gate-manifest.json','identity-audit-contract/joint-composition-proof.json','identity-audit-contract/current-main-integration-proof.json','identity-audit-contract/linux-preparation-observation.json','api-compat/full_native_gate.py']
critical=set(names[:5]+['api-compat/full_native_gate.py'])
payload={'files':[{'relative':name,'content':base64.b64encode((r/name).read_bytes()).decode(),'sha256':hashlib.sha256((r/name).read_bytes()).hexdigest(),'expected_previous_sha256':previous[name] if name in critical else None} for name in names]}
script="""import ast,base64,hashlib,json,pathlib,subprocess,os,stat
r=pathlib.Path('/root/lane-rel-tools');w=pathlib.Path('/root/lane-rel-identity-audit-contract')
def sha(p):return hashlib.sha256(p.read_bytes()).hexdigest()
def git(*a):return subprocess.check_output(['git',*a],cwd=w,text=True).strip()
assert git('rev-parse','HEAD')=='9be01cbbf0144a2f2537667834539de31e40f58f'
assert git('rev-parse','HEAD^{tree}')=='b8aea641ecd1b79be71270ad80ab03c1e131df31'
assert git('status','--porcelain')==''
assert git('rev-parse','refs/lane-rel/native-audit-qualified-783567ea')=='783567ea4a2e19f3b2f7ca065d47dfe59886633e'
assert sha(r/'identity-audit-contract/go-native-linux-proof.json')=='cd648aa45ce2ab13f08838b9254a18b2151ca93c56f0201a3f1f9ca1d5e13cd1'
assert sha(r/'identity-audit-contract/joint-source.bundle')=='e2787456d78729e5ecf7082bf52832cd5abd8012eabc182c0ab28f735a920ce6'
for suffix in ['.log','-proof.json','-generated-go.work.sum']:
 assert not os.path.lexists(r/('api-compat/joint-native-gate-9be01cbb'+suffix))
data=PAYLOAD
for row in data['files']:
 p=r/row['relative'];assert p.parent.is_dir() and p.parent.stat().st_uid==os.geteuid()
 if os.path.lexists(p):assert p.is_file() and not p.is_symlink() and p.stat().st_uid==os.geteuid()
 else:assert row['expected_previous_sha256'] is None
 if row['expected_previous_sha256'] is not None:assert sha(p)==row['expected_previous_sha256'],str(p)+' was changed after the observed preparation state'
 content=base64.b64decode(row['content']);assert hashlib.sha256(content).hexdigest()==row['sha256']
 if p.suffix=='.py':ast.parse(content)
 else:json.loads(content)
for row in data['files']:
 p=r/row['relative'];oldsha=sha(p) if p.exists() else None
 if oldsha is not None:
  archive=p.with_name(p.stem+'.before-joint-preparation-'+oldsha[:12]+p.suffix)
  if not archive.exists():
   with archive.open('xb') as f:f.write(p.read_bytes())
  else:assert sha(archive)==oldsha
 temporary=p.with_name(p.name+'.joint-preparation-new')
 with temporary.open('xb') as f:f.write(base64.b64decode(row['content']))
 os.chmod(temporary,stat.S_IMODE(p.stat().st_mode) if p.exists() else 0o600);os.replace(temporary,p)
 assert sha(p)==row['sha256'];print(row['relative'],row['sha256'],flush=True)
assert git('status','--porcelain')==''
print('SOURCE_CLEAN_FROZEN783_PROOF_UNCHANGED_NO_GATE_ADMISSION_OR_QUEUE',flush=True)
""".replace('PAYLOAD',repr(payload))
cp=subprocess.run(['ssh','-o','ConnectTimeout=15','-o','ServerAliveInterval=10','-o','ServerAliveCountMax=2','ownsolid-cpu','python3 -'],input=script,text=True,stdout=subprocess.PIPE,stderr=subprocess.PIPE,timeout=120)
log=i/'joint-preparation-sync-corrected.log'
with log.open('x') as f:f.write(cp.stdout+cp.stderr)
print(cp.stdout);print(cp.stderr);print('SYNC_EXIT',cp.returncode)
raise SystemExit(cp.returncode)
