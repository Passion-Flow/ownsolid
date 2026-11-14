import json,os,pathlib,re,subprocess,sys,time
os.umask(0o022)
repo=pathlib.Path(sys.argv[1]);root=pathlib.Path(sys.argv[2]);run=pathlib.Path((root/'latest-qualification-path').read_text().strip());proof=json.loads((run/'success.json').read_text());steps=[]
def git(*args):return subprocess.check_output(['git',*args],cwd=repo,text=True).strip()
def execute(cmd,label,timeout=45):
 with (root/('publication-'+label+'.log')).open('wb') as out:r=subprocess.run(cmd,cwd=repo,stdout=out,stderr=subprocess.STDOUT,timeout=timeout)
 steps.append({'name':label,'command':cmd,'exit_code':r.returncode});(root/'publication-checks.json').write_text(json.dumps(steps,indent=2)+'\n');print(label,r.returncode,flush=True)
 if r.returncode:raise SystemExit(label+' failed; original log retained')
if git('status','--porcelain'):raise SystemExit('IAM102_WORKTREE_NOT_CLEAN')
assert proof['adversarial_roots']==8 and proof['actual_provider_tests']==2 and proof['network']=='none' and proof['host_ports']==[]
assert all(s['exit_code']==0 for s in json.loads((run/'results.json').read_text()))
assert (run/'image-license-inventory.json').exists() and (run/'jdk-legal-inventory.json').exists()
container=(run/'container-id').read_text().strip()
if subprocess.run(['docker','inspect',container],capture_output=True).returncode==0:raise SystemExit('IAM102_OWNED_PROVIDER_NOT_CLEANED')
execute(['git','fetch','origin'],'fetch',90)
execute(['git','rebase','origin/main'],'rebase',90)
allowed={'docs/research/oidc-sign-in.md','docs/research/directory-login.md','docs/research/saml-sign-in.md','docs/research/group-role-mapping.md','docs/research/iam-questions.md'}
changed=set(git('diff','--name-only','origin/main..HEAD').splitlines());assert changed<=allowed,changed
note=repo/'docs/research/oidc-sign-in.md';body=note.read_text().split('## Protocol prototype qualification')[0]
body+='''## Protocol prototype qualification

The standalone private-provider prototype passed eight native Go test roots
with race detection, including signed nonce/authorized-party/issuer/audience
and time refusals, duplicate/case-alias fields, exact opaque subjects,
canonical discovery, TLS trust refusals, bounded JWK structure and parsing,
new-key and same-key-ID replacement, unknown/known-key floods, concurrency and
actual request cancellation. Three live-only roots were skipped by that native
unit invocation and exercised separately where applicable below.

Two actual Keycloak 26.8.2 protocol roots then passed in a disposable Linux
arm64 container with `--network none` and no published host ports. The probe
performed the real password form and one S256 authorization-code exchange,
verified the signed realm issuer/nonce/audience and exact nonnumeric subject,
and matched UserInfo to that subject. The client ID and secret contained
characters requiring form encoding before HTTP Basic encoding. Wrong PKCE,
redirect and secret, token issuer/audience/nonce/signature substitution and
consumed-code replay were refused, with no automatic second token exchange.
The separate startup probe also passed.

The fixed image manifest above ran Red Hat OpenJDK 21.0.13. Its complete
application third-party notice, 35 image-package license files and 38 JDK legal
files were retained and read, including the GPL, classpath/assembly and GCC
runtime exceptions and component notices. These materials are for the private
unmodified fixture; they do not admit a provider image for product distribution.
The first LibreSSL implicit-serial pathname failure is retained separately;
explicit serial generation and certificate-chain verification corrected that
fixture setup before the successful provider run.

The durable local evidence set is `'''+root.name+'''`, successful run
`'''+run.name+'''`. It retains exact prototype source hashes, selected module
sums, binary and image identities, native and real-provider logs, independent
request counts, component licenses and cleanup results. The provider and its
owned anonymous storage were removed; the pinned image remains a dependency
cache. No test server remains running.

This establishes the external protocol approach. It adds no product dependency,
connector type, binding, local session, route, database schema or browser UI.
It is neither a rendered-browser nor Linux amd64 installation acceptance.
IAM-102 stays open for the real-flow/storage handoff above and shared owners'
contract, configuration and UI admission. The publication changes research
only; its layout, roadmap and whitespace checks do not replace that future
feature's full repository and installed-flow gates.
'''
note.write_text(body)
questions=repo/'docs/research/iam-questions.md';q=questions.read_text().replace('Qualify the [patched verifier and real private-provider prototype](oidc-sign-in.md), then follow','The [patched verifier and real private-provider prototype passed](oidc-sign-in.md#protocol-prototype-qualification); follow');questions.write_text(q)
execute(['python3','tools/lint/layout/check_layout.py','.'],'layout')
execute(['python3','tools/dev/roadmap_progress.py','--check'],'roadmap')
execute(['git','diff','--check'],'whitespace')
execute(['git','add','--','docs/research/oidc-sign-in.md','docs/research/iam-questions.md'],'stage')
execute(['git','commit','-m','IAM-102: Record qualified private OIDC protocol prototype','-m','Verify eight standalone Go race-test roots and actual pinned network-none Keycloak password, encoded client authentication, S256 exchange and token/UserInfo binding. Retain real refusals, request counts, image/JDK license material and cleanup; keep product schema, contracts, UI and roadmap completion open.'],'commit')
head=git('rev-parse','HEAD');receipt={'head':head,'qualified_protocol_run':run.name,'research_only_paths':sorted(allowed),'pushed':False};(root/'research-publication.json').write_text(json.dumps(receipt,indent=2)+'\n')
execute(['git','fetch','origin'],'final-fetch',90)
if subprocess.run(['git','merge-base','--is-ancestor','origin/main','HEAD'],cwd=repo).returncode:raise SystemExit('IAM102_REBASE_REQUIRED_BEFORE_PUSH')
execute(['git','push','origin','HEAD:main'],'push',90);receipt['pushed']=True;(root/'research-publication.json').write_text(json.dumps(receipt,indent=2)+'\n')
state=json.loads((root/'resume-state.json').read_text());state.update(local_head=head,phase='External OIDC protocol qualified and research published; product IAM-102 remains open',publication_pending=False,qualification_run=str(run),publication=str(root/'research-publication.json'));(root/'resume-state.json').write_text(json.dumps(state,indent=2)+'\n');print('IAM102_PROTOCOL_RESEARCH_PUBLISHED',head,flush=True)
