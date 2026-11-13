"""Record exact CLI qualification in the lane-owned research and status row."""
import hashlib,json,os,subprocess
from pathlib import Path
root=Path('/root/lane-rel-tools/user-reads/native-client-410ed356')
proof=json.loads((root/'native-startup.json').read_text())
assert proof['exit']==0 and proof['cases_passed']==11 and proof['offline_namespace'] is True
source='410ed356ffaf9c5a54a32d9b19e20ea457090f7b'
assert proof['source']==source and proof['binary_sha256']=='e630d9ff65172d6e127a7aa055a5979d2f434abe13eb085cfc1d0ef5d3ece257'
work=Path('/root/lane-rel-users')
def git(*args):return subprocess.check_output(['git','-C',str(work),*args],text=True).strip()
assert git('rev-parse','HEAD')=='f91c7ecbc046a5509dbc6cfb00f1679086993fff'
assert not git('status','--porcelain')
status=work/'docs/product/status.md';s=status.read_text();old='its Linux amd64 static client has an independent build manifest. The installed packages are prepared for the existing DEL window.'
new='its Linux amd64 static client also compiled natively offline from the same clean source, with an identical SHA-256. Eleven Linux startup checks passed in a separate network namespace with only loopback and no routes, using an empty private config. These checks cover version/help, local input refusal and absent credentials. The installed packages still require a newly qualified DEL source and dedicated disposable accounts; the frozen session driver remains bound to its original Control source.'
assert s.count(old)==1;s=s.replace(old,new);status.write_text(s)
research=work/'docs/research/cli.md';s=research.read_text();heading='## Combined account and workspace client qualification (DEV-102)'
assert heading not in s
s+='''
## Combined account and workspace client qualification (DEV-102)

Exact runtime source 410ed356 includes account, workspace-member and own-session
commands. Its complete CLI race suite and vet passed offline on Linux amd64.
With dependency resolution disabled, make check and make build passed on
Darwin arm64. These are separate host scopes; the whole-repository Linux gate
and the complete make test target have not run for this candidate.

The clean source also compiled on Linux amd64 with Go 1.27.3, CGO_ENABLED=0,
-buildvcs=true, -trimpath and one build worker in a network namespace without
external networking. Its 12,101,750-byte static ELF has SHA-256
e630d9ff65172d6e127a7aa055a5979d2f434abe13eb085cfc1d0ef5d3ece257,
identical to the independent Darwin cross-build. BuildInfo retains the exact
revision and vcs.modified=false. Eleven native startup checks passed with a
new network namespace, only loopback, no routes and an empty private config:
version, command help, local argument refusal and refusal without credentials.
No shared Admin account or session was used by those checks.

This qualifies the client artifact and its local startup. Workspace, member,
account and session authorization still require real Admin HTTPS/PostgreSQL
through a newly qualified original DEL installation and dedicated disposable
accounts. Preserve the frozen clients and their Control-source admission; do
not point an old-source acceptance driver at a new schema window. DEV-102
remains open.
'''
research.write_text(s)
subprocess.run(['git','-C',str(work),'diff','--check'],check=True)
subprocess.run(['python3','tools/dev/roadmap_progress.py'],cwd=work,check=True)
subprocess.run(['make','check-layout','check-roadmap'],cwd=work,check=True)
changed=git('diff','--name-only').splitlines();assert set(changed)=={'docs/product/status.md','docs/research/cli.md'},changed
assert not git('diff','--name-only',source,'--','tools/cli')
diff=git('diff','--','docs/product/status.md','docs/research/cli.md');assert len(diff)<16000
subprocess.run(['git','-C',str(work),'add','docs/product/status.md','docs/research/cli.md'],check=True)
subprocess.run(['git','-C',str(work),'commit','-m','DEV-102: Record offline native CLI qualification','-m','Verify exact 410ed356 with offline Linux CLI race/vet, Darwin check/build, an identical native Linux static ELF and eleven isolated startup checks. Preserve installed authorization, whole-repository Linux gates and roadmap boundaries. Layout, roadmap and diff checks pass.'],check=True)
assert not git('status','--porcelain')
head=git('rev-parse','HEAD')
subprocess.run(['git','-C',str(work),'update-ref','refs/lane-rel/user-reads-native-status',head],check=True)
bundle=Path('/root/lane-rel-tools/user-reads-native-status.bundle');assert not bundle.exists()
subprocess.run(['git','-C',str(work),'bundle','create',str(bundle),'refs/lane-rel/user-reads-native-status','^'+source],check=True)
print(json.dumps({'status_commit':head,'runtime_source':source,'bundle':str(bundle),'bundle_sha256':hashlib.sha256(bundle.read_bytes()).hexdigest(),'published_to_main':False,'roadmap_ticked':False}))
