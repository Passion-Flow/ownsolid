import ast,copy,datetime,hashlib,json,pathlib,subprocess
r=pathlib.Path('/Users/passion./.local/state/ownsolid/lane-rel');i=r/'identity-audit-contract';w=i/'work'
source='9be01cbbf0144a2f2537667834539de31e40f58f';base='9e32b023a0a3deed6a8653a8878d52c2583fd6cf';tree='b8aea641ecd1b79be71270ad80ab03c1e131df31';prereq=['8b17a2954260e517b434906e95740a39ecdd4a9a']
def digest(p):return hashlib.sha256(p.read_bytes()).hexdigest()
def load(p):return json.loads(p.read_text())
def write(p,d):p.write_text(json.dumps(d,indent=2)+'\n')
def git(*args):return subprocess.check_output(['git',*args],cwd=w,text=True).strip()
assert git('rev-parse','HEAD')==source and git('rev-parse','HEAD^{tree}')==tree and not git('status','--porcelain')
paths=['identity-audit-contract/handoff.json','identity-audit-contract/relay/handoff.json','api-compat/handoff.json','dev102-current-handoff.json','identity-audit-contract/joint-full-gate-manifest.json','identity-audit-contract/joint-composition-proof.json','identity-audit-contract/current-main-integration-proof.json','identity-audit-contract/linux-preparation-observation.json']
previous={name:digest(r/name) for name in paths};previous['api-compat/full_native_gate.py']='dd3fb9294b7477e863f260a6ee35bc2ee22ac9e80294322cf759b3ba0ff12f50'
write(i/'joint-preparation-sync-previous-sha256.json',previous)
for name in paths:
 p=r/name;b=p.with_name(p.stem+'.before-9be-preparation-'+previous[name][:8]+p.suffix)
 if not b.exists():b.write_bytes(p.read_bytes())
 else:assert b.read_bytes()==p.read_bytes()
prep=load(i/'linux-source-toolchain-preparation.json');browser=load(i/'linux-browser-cache-preparation.json')
assert prep['source']==source and prep['source_clean_after_cleanup'] and browser['source']==source and browser['source_clean_after']
libs='/root/lane-rel-tools/playwright-libs.tUcBz3Xq/sysroot/usr/lib/x86_64-linux-gnu'
for row in browser['checks']:
 selected=next(x for x in row['ldd'] if x['library_directory']==libs)
 assert selected['exit']==0 and selected['missing']==[]
entry=r/'api-compat/full_native_gate.py';ast.parse(entry.read_text());entrysha=digest(entry)
proofs={name:{'path':'/root/lane-rel-tools/identity-audit-contract/'+name,'sha256':digest(i/name)} for name in ['linux-source-toolchain-preparation.json','linux-browser-cache-preparation.json']}
manifest=load(i/'joint-full-gate-manifest.json')
assert git('diff','--name-only',base,'HEAD').splitlines()==list(manifest['changed_source_files'])
assert all(digest(w/name)==sha for name,sha in manifest['changed_source_files'].items())
assert all(digest(w/name)==sha for name,sha in manifest['protected_consumer_files'].items())
manifest.update(entry_sha256=entrysha,server_checkout_staged=True,server_source_clean=True,source_preparation_ready=True,ready_for_execution=False,platform_boundary='Linux exact-source preparation is complete. Execution still requires the preceding TRN actual result/release and root assignment of this full resource segment.',offline_cache_preparation=proofs,browser_environment={'PLAYWRIGHT_BROWSERS_PATH':'/root/lane-rel-tools/playwright-browsers','LD_LIBRARY_PATH':libs},generated_workspace_sum_cleanup='Archive only a newly generated owned regular go.work.sum outside the checkout, retain its SHA256, then remove that generated file before checking the source is clean. Existing source files are never removed.',original_make_commands=[['make',x] for x in ['web-install','check','build','test']],invocation_shell_lines=['umask 077','set -C','. /root/lane-rel-tools/env','python3 /root/lane-rel-tools/api-compat/full_native_gate.py > /root/lane-rel-tools/api-compat/joint-native-gate-9be01cbb.log 2>&1'])
manifest['prerequisites']=['Root confirms the actual result/release of the preceding TRN short segment and assigns this final exact entry its full segment','Invoke the self-admission entry only once with exclusive log creation, without an outer lock, waits or retries','If actual main changes before publication, reconcile permitted author changes and rebind the exact source, rerunning affected checks as needed']
write(i/'joint-full-gate-manifest.json',manifest)
composition=load(i/'joint-composition-proof.json')
composition.update(joint_source=source,tree=tree,main_base=base,changed_file_sha256=manifest['changed_source_files'],documentation_only_after_generation=False,generation_inputs_unchanged_after_main_merge=True,current_main_integration_proof='/root/lane-rel-tools/identity-audit-contract/current-main-integration-proof.json',bundle={'path':str(i/'joint-source.bundle'),'sha256':digest(i/'joint-source.bundle'),'bytes':(i/'joint-source.bundle').stat().st_size,'prerequisites':prereq,'ref':'HEAD','server_bundle_verify_and_import_passed':True,'independent_bare_import_verified':False,'alternate_objects_directory':False})
write(i/'joint-composition-proof.json',composition)
integration=load(i/'current-main-integration-proof.json');integration.update(entry_sha256=entrysha,linux_staged_exact_source=True,linux_clean_after_preparation=True,linux_preparation_proofs=proofs);write(i/'current-main-integration-proof.json',integration)
observation=load(i/'linux-preparation-observation.json');observation.update(toolchain_inventory_complete=True,offline_caches_complete_for_preparation=True,source_clean_after_cleanup=True,preparation_failure_resolved=True,resolution='The pinned pnpm12 wrapper is the upstream shebangless placeholder, which works through the existing shell wrapper with its installed Linux x64 native binary. The original direct-Popen version-probe errno8 is retained as a preparation invocation error. Go metadata generated go.work.sum was archived and removed. The default browser cache was absent; the existing pinned1248 cache and complete own shared-library cache have both binaries with no missing ldd dependencies.',actual_offline_pnpm_install={'exit':0,'reused':413,'downloaded':0,'ignore_scripts':True},proofs=proofs);write(i/'linux-preparation-observation.json',observation)
h=load(i/'handoff.json')
h.update(source=source,tree=tree,base=base,bundle_sha256=digest(i/'joint-source.bundle'),bundle_prerequisites=prereq,portable_independent_bare_import_verified=False,bundle_import_verification='Current bundle was verified and imported into the existing owned Linux repository with its8b17a295 prerequisite; the prior independent-bare test belongs only to the previous bundle.',file_sha256=manifest['changed_source_files'],joint_composition_proof_sha256=digest(i/'joint-composition-proof.json'),full_gate_manifest_sha256=digest(i/'joint-full-gate-manifest.json'),prepared_native_entry_source=source,prepared_native_entry_sha256=entrysha,source_parents=git('show','-s','--format=%P','HEAD').split(),base_kind='Observed current-main integration baseline; bundle prerequisite8b17a295 is recorded separately.',linux_worktree='/root/lane-rel-identity-audit-contract',linux_staged_source=source,linux_source_clean=True,source_preparation_ready=True,linux_preparation_proofs=proofs,current_main_integration_proof='/root/lane-rel-tools/identity-audit-contract/current-main-integration-proof.json',current_main_integration_proof_sha256=digest(i/'current-main-integration-proof.json'),full_gate_ready=False)
h['required_remaining_gates']=['Root confirms the actual TRN short-segment result/release and assigns this prepared exact Linux entry the full segment','Run complete original web-install/check/build/test under the existing strict two-carrier self-admission entry; no overlay, target skip, outer lock, wait or retry','Reconcile any later actual main change, self-review, and perform one authorized fast-forward publication only after required checks pass']
h['native_runtime_bindings']['frozen_source_retained_ref']='refs/lane-rel/native-audit-qualified-783567ea'
write(i/'handoff.json',h)
relay=load(i/'relay/handoff.json')
relay.update(source=source,composition_source=source,contract_source=source,main_base=base,source_bundle_sha256=h['bundle_sha256'],bundle_prerequisites=prereq,source_parents=h['source_parents'],current_handoff_sha256=digest(i/'handoff.json'),patch_base=base)
patch=subprocess.check_output(['git','diff','--binary','--abbrev=7',base,'HEAD','--',*relay['generated_paths']],cwd=w)
assert hashlib.sha256(patch).hexdigest()==relay['generated_patch_sha256']
relay['handoff_sequence']=['DEV47fa and the original-author WEBc7 five-path replaya84 are composed, excluding unrelated researchf1','Actual generation on871e and tooling tests on2c20 remain input-qualified; the merge of current main9e adds published model domain code and does not change the79 projection inputs,122 SDK files or five protected consumers','Exact Linux9be preparation is complete; execute the original full gate only after the actual preceding TRN result/release and root resource-segment assignment, then publish one qualified combined result']
write(i/'relay/handoff.json',relay)
a=load(r/'api-compat/handoff.json');a.update(composed_into=source,prepared_gate=manifest,current_source_delivery_handoff_sha256=digest(i/'handoff.json'));a['native_gate_status']['prepared_entry_source']=source;write(r/'api-compat/handoff.json',a)
d=load(r/'dev102-current-handoff.json');frozen={k:copy.deepcopy(d[k]) for k in ['frozen_workspace','session','publication_candidate','model_history_contract']}
c=d['identity_audit_contract_candidate'];c.update(source=source,tree=tree,base=base,handoff_sha256=digest(i/'handoff.json'),bundle_sha256=h['bundle_sha256'],bundle_prerequisites=prereq,web_relay_sha256=digest(i/'relay/handoff.json'),full_gate_manifest_sha256=digest(i/'joint-full-gate-manifest.json'),source_preparation_ready=True,linux_preparation_proofs=proofs)
d['api_compatibility_candidate'].update(composition_candidate_source=source,handoff_sha256=digest(r/'api-compat/handoff.json'));d['api_compatibility_candidate']['native_gate_status']['prepared_entry_source']=source
boundary='Exact joint9be01cbb/treeb8aea641 on main9e32b023 retains original DEV47fa and original-author WEBc7 replaya84, excluding unpublishedf1. The current-main merge includes published MODEL family/history domain code, unnumbered candidate DDL and ADR0081; private6a2 remains excluded from public API. The owned delta is18 paths;79 projection inputs,122 SDK files and five protected consumers match the prior actual generation/tooling inputs. Actual430 audit remains exit1 for the explicit409 binding review. Linux source staging, Go/Cargo metadata, offline pnpm install413 reused/0download and pinned1248 browser/shared-library metadata preparation passed; no browser, build, full gate, lock admission or queue was started. Source9be remains unqualified until the full original gate after the actual preceding TRN segment release and root assignment. Frozen783 native evidence, combined-parent exit1 and private history0/NOTRUN remain unchanged; no installed or CLIb967 qualification and no publication.'
d.update(current_work_boundary=boundary,updated_utc=datetime.datetime.now(datetime.timezone.utc).isoformat())
assert all(d[k]==v for k,v in frozen.items());write(r/'dev102-current-handoff.json',d)
for name in paths+['api-compat/full_native_gate.py']:
 print(name,digest(r/name))
print('PREPARATION_ONLY_NO_GATE_OR_ADMISSION')
