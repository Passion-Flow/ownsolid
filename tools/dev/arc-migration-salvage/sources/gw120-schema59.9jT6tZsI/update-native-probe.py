from pathlib import Path
import base64,hashlib,shlex,subprocess
p=Path(__file__).resolve().parent;remote=(p/'remote').read_text().strip()
files={name:base64.b64encode((p/name).read_bytes()).decode() for name in ['stale_snapshot_rollback_test.go','qualify-stale-snapshot.sh']}
code="""from pathlib import Path
import base64,subprocess,json
p=Path(%r);assert not (p/'repo').exists();assert (p/'exit').read_text().strip()=='75'
for name,raw in %r.items(): (p/name).write_bytes(base64.b64decode(raw));(p/name).chmod(0o600)
build=(p/'build-offline.sh').read_text().replace("test =", "test =")
build=build.replace("str(p/name)}}))", "str(p/name),str(p/'repo/services/control/internal/modules/distribution/stale_snapshot_rollback_test.go'):str(p/'stale_snapshot_rollback_test.go')}}))")
build=build.replace('test -p 1 -c -o "$work/distribution.test"','test -p 1 -overlay "$work/matrix-overlay.json" -c -o "$work/distribution.test"')
(p/'build-offline.sh').write_text(build)
replay=(p/'replay.sh').read_text()+"\\nsource \\\"$work/qualify-stale-snapshot.sh\\\"\\n"
(p/'replay.sh').write_text(replay)
subprocess.run(['bash','-n',str(p/'replay.sh'),str(p/'build-offline.sh')],check=True)
(p/'first-preflight-exit').write_text((p/'exit').read_text());(p/'exit').unlink()
with (p/'run-2.log').open('wb') as log:
 child=subprocess.Popen(['bash',str(p/'guard.sh')],stdin=subprocess.DEVNULL,stdout=log,stderr=subprocess.STDOUT,start_new_session=True)
(p/'pid').write_text(str(child.pid)+'\\n')
print('Added isolated stale-snapshot probe; restarted resource admission '+str(child.pid))
"""%(remote,files)
with (p/'probe-upload.log').open('wb') as out:
 r=subprocess.run(['ssh','-o','ConnectTimeout=25','-o','ServerAliveInterval=10','-o','ServerAliveCountMax=2','ownsolid-cpu','python3 -c '+shlex.quote(code)],stdin=subprocess.DEVNULL,stdout=out,stderr=subprocess.STDOUT,timeout=65)
print((p/'probe-upload.log').read_text());raise SystemExit(r.returncode)
