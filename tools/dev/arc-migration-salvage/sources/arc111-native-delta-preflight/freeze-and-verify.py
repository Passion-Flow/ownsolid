from pathlib import Path
import ast
import hashlib
import importlib.util
import io
import json
import os
import select
import subprocess
import tarfile
import tempfile
import threading
import time

os.umask(0o077)
out = Path(__file__).resolve().parent
diagnostics = out.parent
repo = diagnostics.parent / 'lane-arc111-qualified-readers'
fix = diagnostics / 'arc111-native-observed.eldemtbg/post-entry-fix'
source = '3513ab2ba5885bb5d782919f7611d96e35d28fc6'
payload = json.loads((out / 'payload-manifest.json').read_text())
policy_text = (out / 'transport-policy.py').read_text()
policy = {}
exec(compile(policy_text, 'transport-policy', 'exec'), policy)
receiver_ast = ast.parse((fix / 'remote-guard.corrected-source-bytes').read_text())
receive_def = next(node for node in receiver_ast.body if isinstance(node, ast.FunctionDef) and node.name == 'receive_payload')
receive_namespace = {'hashlib': hashlib}
exec(compile(ast.Module(body=[receive_def], type_ignores=[]), 'original-receiver', 'exec'), receive_namespace)
receive = receive_namespace['receive_payload']
wire = (out / 'payload.wire.xz').read_bytes()
assert len(wire) == payload['bytes'] and hashlib.sha256(wire).hexdigest() == payload['sha256']

# The real sender and receiver are exercised on the complete actual wire frame.
pipe_r, pipe_w = os.pipe()
os.set_blocking(pipe_r, False)
os.set_blocking(pipe_w, False)
send_receipt = {}
receive_receipt = {}
errors = []
started = time.monotonic()


def writer():
    try:
        policy['send_payload'](lambda data: os.write(pipe_w, data),
                               lambda remaining: bool(select.select([], [pipe_w], [], remaining)[1]),
                               time.monotonic, started + 45, wire, send_receipt)
    except BaseException as error:
        errors.append(type(error).__name__ + ':' + str(error))
    finally:
        os.close(pipe_w)
        send_receipt['pipe_closed'] = True


thread = threading.Thread(target=writer, name='ARC_FULL_FRAME_LOCAL_PIPE_WRITER')
thread.start()
try:
    received = receive(lambda count: os.read(pipe_r, count),
                       lambda remaining: bool(select.select([pipe_r], [], [], remaining)[0]),
                       time.monotonic, started + 45, len(wire), payload['sha256'], receive_receipt)
finally:
    os.close(pipe_r)
    thread.join(timeout=2)
assert not thread.is_alive() and not errors
assert received == wire and send_receipt['written_bytes'] == receive_receipt['received_bytes'] == len(wire)
assert send_receipt['written_sha256'] == receive_receipt['received_sha256'] == payload['sha256']
assert receive_receipt['eof_observed'] and send_receipt['pipe_closed']

# The decoded envelope and every original SQL member are independently bound.
raw = policy['decode_envelope'](received, payload['transport'])
prototype = Path(tempfile.mkdtemp(prefix='local-source-proof.', dir=out))
policy['unpack_members'](raw, prototype, payload['members'])
manifest = json.loads((prototype / 'source-manifest.json').read_text())
transport_binding = json.loads((prototype / 'transport-binding.json').read_text())
basis = Path(tempfile.mkdtemp(prefix='local-readonly-basis.', dir=out))
basis_revision = transport_binding['basis_source']
basis_archive = subprocess.check_output(['git', 'archive', '--format=tar', basis_revision], cwd=repo)
with tarfile.open(fileobj=io.BytesIO(basis_archive), mode='r:') as archive:
    for member in archive:
        if member.isdir():
            continue
        policy['checked_relative'](member.name)
        assert member.isfile()
        path = basis / member.name
        path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        with path.open('xb') as stream:
            stream.write(archive.extractfile(member).read())
        assert member.mode in {0o664, 0o775}
        os.chmod(path, member.mode & ~0o020)

apply_results = []


def git_apply(destination, patch):
    began = time.monotonic()
    result = subprocess.run(['git', '--no-optional-locks', '-c', 'core.hooksPath=/dev/null',
                             'apply', '--binary', '--whitespace=nowarn', '--', str(patch)],
                            cwd=destination, capture_output=True, timeout=10, umask=0o022)
    apply_results.append({'exit': result.returncode, 'elapsed_seconds': time.monotonic() - began,
                          'stdout_sha256': hashlib.sha256(result.stdout).hexdigest(),
                          'stderr_sha256': hashlib.sha256(result.stderr).hexdigest()})
    (out / 'local-git-apply.stderr').write_bytes(result.stderr)
    assert result.returncode == 0, 'LOCAL_GIT_PATCH_FAILED'


proof_begin = time.monotonic()
proof = policy['reconstruct_source'](prototype, manifest, transport_binding, basis, os.getuid(), proof_begin + 45, git_apply)
proof_seconds = time.monotonic() - proof_begin
assert proof['target_paths'] == 5102 and proof['basis_file_inputs_verified'] == 5015
input_data = json.loads((prototype / 'migration-inputs.json').read_text())
for item in input_data['migrations']:
    assert hashlib.sha256((prototype / 'source' / item['path']).read_bytes()).hexdigest() == item['sha256']
for item in input_data['inputs']:
    data = (prototype / item['path']).read_bytes()
    assert len(data) == item['bytes'] and hashlib.sha256(data).hexdigest() == item['sha256']

# A cache failure must never synthesize a source pass or enter Git application.
sample = next(ref for ref in manifest['files'] if ref['path'] not in transport_binding['delta_paths'])
bad = dict(sample, sha256='0' * 64)
negative = []
for label, name, ref in [('wrong_hash', sample['path'], bad),
                          ('missing_file', 'arc111-does-not-exist', sample)]:
    try:
        policy['read_cached_file'](basis, name, ref, os.getuid())
    except (AssertionError, FileNotFoundError) as error:
        negative.append({'case': label, 'refused': True, 'type': type(error).__name__})
    else:
        raise AssertionError('NEGATIVE_CACHE_CASE_NOT_REFUSED')
symlink = basis / 'arc111-owned-symlink'
symlink.symlink_to(basis / sample['path'])
try:
    try:
        policy['read_cached_file'](basis, symlink.name, sample, os.getuid())
    except OSError as error:
        negative.append({'case': 'symlink', 'refused': True, 'type': type(error).__name__})
    else:
        raise AssertionError('SYMLINK_NOT_REFUSED')
finally:
    symlink.unlink()

# Freeze an independent private entry; no old closed launcher is reopened.
guard = (fix / 'remote-guard.corrected-source-bytes').read_text()
guard = guard.replace('import os,sys,json,time,stat,hashlib,fcntl,subprocess,signal,shutil,re,tempfile,tarfile,io,socket,secrets,uuid,base64,select',
                      'import os,sys,json,time,stat,hashlib,fcntl,subprocess,signal,shutil,re,tempfile,tarfile,io,socket,secrets,uuid,base64,select,lzma')
guard = guard.replace('from pathlib import Path', 'from pathlib import Path, PurePosixPath', 1)
guard = guard.replace('def command(name,argv,data=None,timeout=20,allow=False,cleanup=False):',
                      'def command(name,argv,data=None,timeout=20,allow=False,cleanup=False,cwd=None):')
guard = guard.replace('start_new_session=True,close_fds=True);children.append(p)',
                      "start_new_session=True,close_fds=True,cwd=cwd,umask=0o022 if name=='source-git-apply' else -1);children.append(p)")
helper_tree = ast.parse(policy_text)
helpers = '\n\n'.join(ast.get_source_segment(policy_text, node) for node in helper_tree.body if isinstance(node, ast.FunctionDef))
guard = guard.replace('def terminate(signum,frame):', helpers + '\n\ndef terminate(signum,frame):')
begin = guard.index(" raw=read_admission_payload(")
finish = guard.index(" phase_end=min(work_end,time.monotonic()+60)", begin)
replacement = ''' record['active_stage']='wire_receive';save()
 raw=read_admission_payload(cfg['payload_bytes'],cfg['payload_sha256'])
 assert len(raw)==cfg['payload_bytes'] and hashlib.sha256(raw).hexdigest()==cfg['payload_sha256'],'PAYLOAD_REFUSAL'
 emit('INPUT',record['payload_receive'])
 record['active_stage']='decode_frame';save()
 decoded=decode_envelope(raw,cfg['transport'])
 record['decoded_frame']={'bytes':len(decoded),'sha256':hashlib.sha256(decoded).hexdigest()};save()
 record['active_stage']='parse_members';save()
 unpack_members(decoded,root,cfg['payload_members'])
 manifest=json.loads((root/'source-manifest.json').read_text());assert manifest['source']==cfg['source'] and manifest['tree']==cfg['tree'] and len(manifest['files'])==5102
 assert hashlib.sha256((root/'source-manifest.json').read_bytes()).hexdigest()==cfg['source_manifest_sha256']
 transport_binding=json.loads((root/'transport-binding.json').read_text())
 assert transport_binding['target_source']==cfg['source'] and transport_binding['target_tree']==cfg['tree']
 record['active_stage']='source_reconstruction';save()
 source_started=time.monotonic()
 def apply_source_delta(destination,patch):
  command('source-git-apply',['git','--no-optional-locks','-c','core.hooksPath=/dev/null','apply','--binary','--whitespace=nowarn','--',str(patch)],timeout=10,cwd=str(destination))
 record['source_reconstruction']=reconstruct_source(root,manifest,transport_binding,Path(transport_binding['basis_path']),0,min(phase_end,work_end),apply_source_delta)
 record['source_reconstruction']['elapsed_seconds']=time.monotonic()-source_started
 input_data=json.loads((root/'migration-inputs.json').read_text())
 for m in input_data['migrations']:assert hashlib.sha256((root/'source'/m['path']).read_bytes()).hexdigest()==m['sha256']
 for m in input_data['inputs']:
  data=(root/m['path']).read_bytes();assert len(data)==m['bytes'] and hashlib.sha256(data).hexdigest()==m['sha256']
 catalog_go=(root/'source/services/control/internal/platform/database/catalog.go').read_bytes()
 assert re.search(rb'const CatalogSQL = `([\\s\\S]*?)`',catalog_go)[1]+b';\\n'==(root/'metadata-inputs/catalog.sql').read_bytes()
 record['all_source_paths_and_migration_inputs_verified']=True;record['native_binaries_locally_verified_not_executed']=json.loads((root/'binding.json').read_text())['native_binaries_locally_verified'];save()
 emit('SOURCE',record['source_reconstruction'])
 assert time.monotonic()<phase_end,'ADMISSION_PHASE_EXCEEDED'
 record['active_stage']='fixture';save()
'''
guard = guard[:begin] + replacement + guard[finish:]
guard = guard.replace(" phase_end=min(work_end,time.monotonic()+180)", " record['active_stage']='catalog_metadata';save()\n phase_end=min(work_end,time.monotonic()+180)")
guard = guard.replace(" record['error_type']=type(error).__name__;record['error']=str(error)[:2000]",
                      " record['error_type']=type(error).__name__;record['error']=str(error)[:2000];record['first_failure_stage']=record.get('active_stage','preflight_or_carrier')")
ast.parse(guard)
(out / 'remote-guard.frozen-source-bytes').write_text(guard)

driver = (fix / 'driver.corrected-source-bytes').read_text()
driver = driver.replace('import subprocess,json,time,hashlib,shlex,base64,os,signal,tarfile,io,sys,threading',
                        'import subprocess,json,time,hashlib,shlex,base64,os,signal,tarfile,io,sys,threading,select')
sender = ast.get_source_segment(policy_text, next(node for node in helper_tree.body if isinstance(node, ast.FunctionDef) and node.name == 'send_payload'))
driver = driver.replace('def remaining_work_timeout', sender + '\n\ndef remaining_work_timeout', 1)
driver = driver.replace("cfg['remote_budget_seconds']=min(cfg['remote_budget_seconds'],1000)",
                        "cfg['remote_budget_seconds']=min(cfg['remote_budget_seconds'],1000)\ncfg['transport']={k:v for k,v in payload['transport'].items() if k!='source_transport'}")
driver = driver.replace("out=Path(__file__).resolve().parent;start=time.monotonic();end=start+1200;work_end=end-120", """out=Path(__file__).resolve().parent
entry_fd=os.open(out/'entry-consumed',os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
os.write(entry_fd,b'One authorized opportunity; no retries.\\n');os.close(entry_fd)
start=time.monotonic();end=start+1200;work_end=end-120""")
driver = driver.replace("record['payload_requested_bytes']=len(payload_bytes)", "record['payload_requested_bytes']=len(payload_bytes)\nassert len(payload_bytes)==payload['bytes'] and hashlib.sha256(payload_bytes).hexdigest()==payload['sha256']")
driver = driver.replace("(out/'payload.tar').read_bytes()", "(out/'payload.wire.xz').read_bytes()")
driver = driver.replace("EOF delivered by communicate", "local OS-write counts; explicit pipe close; remote EOF separately verified")
driver = driver.replace("  child.communicate(payload_bytes,timeout=remaining_work_timeout(time.monotonic(),work_end,1100));record['ssh_exit']=child.returncode", '''  send_receipt=record.setdefault('payload_send',{})
  os.set_blocking(child.stdin.fileno(),False)
  try:
   send_payload(lambda data:os.write(child.stdin.fileno(),data),lambda remaining:bool(select.select([],[child.stdin.fileno()],[],remaining)[1]),time.monotonic,work_end,payload_bytes,send_receipt)
  except (OSError,ValueError,TimeoutError) as error:
   record['payload_send_error_type']=type(error).__name__;record['payload_send_error']=str(error)[:240]
  finally:
   child.stdin.close();send_receipt['pipe_closed']=True
   (out/'outer-result.json').write_text(json.dumps(record,indent=2)+'\\n')
  child.wait(timeout=remaining_work_timeout(time.monotonic(),work_end,1100));record['ssh_exit']=child.returncode''')
driver = driver.replace('"remote_result_received", b"ARC_NATIVE_RESULT "', '"remote_result_received", b"ARC_NATIVE_RESULT "')
driver = driver.replace('("remote_carrier_admitted", b"ARC_NATIVE_ROOT "),', '("remote_carrier_admitted", b"ARC_NATIVE_ROOT "), ("payload_received", b"ARC_NATIVE_INPUT "), ("source_verified", b"ARC_NATIVE_SOURCE "),')
driver = driver.replace("'within_budget','observer_joined','observed_stage_events'", "'within_budget','observer_joined','observed_stage_events','payload_send'")
driver = driver.replace('    remote = record.get("remote")', '''    sent = record.get("payload_send", {})
    if sent.get("written_bytes") != record.get("payload_requested_bytes") or not sent.get("pipe_closed") or sent.get("error"):
        return 1
    remote = record.get("remote")''', 1)
ast.parse(driver)
(out / 'driver.frozen-source-bytes').write_text(driver)

result = {'source': source, 'tree': manifest['tree'], 'wire_bytes': len(wire),
          'wire_sha256': payload['sha256'], 'sender': send_receipt, 'receiver': receive_receipt,
          'full_frame_real_os_pipe': True, 'sender_thread_joined': not thread.is_alive(),
          'decoded_bytes': len(raw), 'decoded_sha256': hashlib.sha256(raw).hexdigest(),
          'source_reconstruction': proof, 'source_reconstruction_seconds': proof_seconds,
          'source_apply': apply_results, 'original_migration_inputs_verified': len(input_data['migrations']),
          'original_sql_members_verified': len(input_data['inputs']), 'negative_cache_cases': negative,
          'transport_policy_sha256': hashlib.sha256(policy_text.encode()).hexdigest(),
          'remote_guard_sha256': hashlib.sha256(guard.encode()).hexdigest(),
          'driver_sha256': hashlib.sha256(driver.encode()).hexdigest(),
          'proof_scope': 'Offline complete byte/pipe/source equivalence only; no SSH, SQL, native execution or product qualification.',
          'no_network_or_compiler': True}
(out / 'full-frame-offline-result.json').write_text(json.dumps(result, indent=2, sort_keys=True) + '\n')
print(json.dumps(result, indent=2))
