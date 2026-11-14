from pathlib import Path
import ast,hashlib,json,difflib
out=Path(__file__).resolve().parent;base=out.parent;prior=base/'arc111-native-formal61.qtc543lk'
original=(prior/'run-once.executed-source-bytes').read_text()
helpers='''
def remaining_work_timeout(now, work_end, maximum):
    remaining = work_end - now
    if remaining <= 0:
        raise TimeoutError("ARC_WORK_DEADLINE_EXHAUSTED")
    return min(maximum, remaining)

def remaining_cleanup_timeout(now, end, maximum):
    return max(0, min(maximum, max(0, end - now)))

def observed_ssh_stages(stderr):
    import re
    text = stderr.decode("utf-8", errors="replace")
    markers = {
        "proxy_command_started": "Executing proxy command:",
        "local_version_emitted": "Local version string SSH-",
        "remote_banner_received": "Remote protocol version",
        "kexinit_received": "SSH2_MSG_KEXINIT received",
        "authenticated": "Authenticated to ",
        "remote_command_sent": "Sending command:",
    }
    stages = {key: True if marker in text else None for key, marker in markers.items()}
    stages["host_key_matched"] = True if re.search(r"is known and matches the .* host key", text) else None
    errors = []
    for line in text.splitlines():
        if any(needle in line for needle in ["timed out", "Connection refused", "Permission denied", "Host key verification failed", "Could not resolve hostname", "failed to connect to local tailscaled"]):
            errors.append(re.sub(r"[^\\x20-\\x7e]", "?", line)[:240])
    return {"stages": stages, "necessary_errors": errors[:6], "missing_marker_means": "unknown, not proof that a stage was not entered"}

def forwarded_outer_exit(record):
    if record.get("outer_timeout"):
        return 124
    status = record.get("ssh_exit")
    if type(status) is not int:
        return 1
    if status != 0:
        return min(255, 128 - status) if status < 0 else min(255, status)
    remote = record.get("remote")
    if not isinstance(remote, dict) or remote.get("state") != "METADATA_COMPLETE_REBUILD_REQUIRED":
        return 1
    if remote.get("source") != record.get("source") or remote.get("tree") != record.get("tree"):
        return 1
    if not all(remote.get(key) is True for key in ["all_source_paths_and_migration_inputs_verified", "container_absent", "volume_absent", "network_absent", "credential_paths_absent", "lock_identities_retained", "within_remote_budget"]):
        return 1
    if remote.get("remaining_owned_groups") != [] or not record.get("within_budget"):
        return 1
    closed = remote.get("lock_fd_closure")
    if not isinstance(closed, list) or len(closed) != 2 or any(item.get("closed") is not True for item in closed):
        return 1
    return 0
'''
assert 'raise SystemExit' not in original
candidate=original.replace('import subprocess,json,time,hashlib,shlex,base64,os,signal,tarfile,io','import subprocess,json,time,hashlib,shlex,base64,os,signal,tarfile,io,sys')
candidate=candidate.replace('out=Path(__file__).resolve().parent;start=time.monotonic();end=start+1200',helpers+'\nout=Path(__file__).resolve().parent;start=time.monotonic();end=start+1200;work_end=end-120')
candidate=candidate.replace("guard=(out/'remote-guard.py').read_bytes();", "cfg['remote_budget_seconds']=min(cfg['remote_budget_seconds'],1000)\nguard=(out/'remote-guard.py').read_bytes();")
candidate=candidate.replace("command=['ssh',", "command=['/usr/bin/ssh','-vv','-o','BatchMode=yes',")
candidate=candidate.replace("record={'source':binding['source']", "payload_bytes=(out/'payload.tar').read_bytes()\nrecord={'source':binding['source']")
candidate=candidate.replace("with (out/'transport.stdout').open('wb') as stdout", "record['launcher_context']={'cwd':str(Path.cwd()),'uid':os.getuid(),'ssh_exec':str(Path(command[0]).resolve()),'ssh_exec_sha256':hashlib.sha256(Path(command[0]).read_bytes()).hexdigest(),'ssh_config_sha256':hashlib.sha256((Path.home()/'.ssh/config').read_bytes()).hexdigest(),'stdin_mode':'PIPE; payload preserved; EOF delivered by communicate','ssh_auth_sock_present':bool(os.environ.get('SSH_AUTH_SOCK')),'ssh_askpass_present':bool(os.environ.get('SSH_ASKPASS'))}\ninvocation=out/'ssh-invocation.json';invocation.write_text(json.dumps(command)+'\\n');record['ssh_invocation_sha256']=hashlib.sha256(invocation.read_bytes()).hexdigest()\nremaining_work_timeout(time.monotonic(),work_end,1100)\nwith (out/'transport.stdout').open('wb') as stdout")
candidate=candidate.replace("child.communicate((out/'payload.tar').read_bytes(),timeout=1100)", "child.communicate(payload_bytes,timeout=remaining_work_timeout(time.monotonic(),work_end,1100))")
candidate=candidate.replace('except subprocess.TimeoutExpired:', 'except (subprocess.TimeoutExpired,TimeoutError):',1)
candidate=candidate.replace('child.wait(timeout=20)','child.wait(timeout=remaining_cleanup_timeout(time.monotonic(),end,20))')
candidate=candidate.replace('child.wait(timeout=10)','child.wait(timeout=remaining_cleanup_timeout(time.monotonic(),end,10))')
candidate=candidate.replace("record['ssh_stderr']=(out/'transport.stderr').read_text(errors='replace')[-2000:]", "record['ssh_observation']=observed_ssh_stages((out/'transport.stderr').read_bytes())\nrecord['ssh_stderr_sha256']=hashlib.sha256((out/'transport.stderr').read_bytes()).hexdigest()")
candidate+='\nraise SystemExit(forwarded_outer_exit(record))\n'
ast.parse(candidate)
original_path=out/'run-once.original-source-bytes'
if original_path.exists():assert original_path.read_text()==original
else:original_path.write_text(original)
(out/'run-once.corrected-source-bytes').chmod(0o600)
(out/'run-once.corrected-source-bytes').write_text(candidate)
(out/'run-once.correction.patch').write_text(''.join(difflib.unified_diff(original.splitlines(True),candidate.splitlines(True),fromfile='run-once.original-source-bytes',tofile='run-once.corrected-source-bytes')))
remote=(prior/'remote-guard.py').read_text();revised=remote.replace('end=start+1100; work_end=end-120','end=start+1000; work_end=end-120').replace("'remote_budget_seconds':1100","'remote_budget_seconds':1000").replace('cleanup_begin=time.monotonic();remaining=[]','cleanup_begin=time.monotonic();end=min(end,cleanup_begin+120);remaining=[]').replace("record['within_remote_budget']=record['elapsed_seconds']<=1100","record['within_remote_budget']=record['elapsed_seconds']<=cfg['remote_budget_seconds']")
revised=revised.replace('end=start+1000; work_end=end-120', "assert 120<cfg['remote_budget_seconds']<=1000; end=start+cfg['remote_budget_seconds']; work_end=end-120").replace("'remote_budget_seconds':1000","'remote_budget_seconds':cfg['remote_budget_seconds']")
assert revised!=remote;ast.parse(revised)
(out/'remote-guard.corrected-source-bytes').chmod(0o600)
(out/'remote-guard.corrected-source-bytes').write_text(revised)
(out/'remote-guard.correction.patch').write_text(''.join(difflib.unified_diff(remote.splitlines(True),revised.splitlines(True),fromfile='remote-guard.original-source-bytes',tofile='remote-guard.corrected-source-bytes')))
for name in ['run-once.original-source-bytes','run-once.corrected-source-bytes','remote-guard.corrected-source-bytes']:(out/name).chmod(0o400)
print(json.dumps({'original_driver_sha256':hashlib.sha256(original.encode()).hexdigest(),'corrected_driver_sha256':hashlib.sha256(candidate.encode()).hexdigest(),'corrected_remote_guard_sha256':hashlib.sha256(revised.encode()).hexdigest(),'active_entry_unchanged':(prior/'run-once.py').read_text(),'candidate_activation':'Data-only frozen source; not installed, no new network call'},indent=2))
