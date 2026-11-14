from pathlib import Path
import os,subprocess,tempfile,time
base=Path(__file__).resolve().parent
repo=Path.cwd().resolve();lock=Path('/private/tmp/ownsolid-workstation-full-gate.lock')
run=Path(tempfile.mkdtemp(prefix='mac-full-gate.',dir=str(base)))
script=Path('../lane-arc-diagnostics/audit58-resume.UeFZoBlF/current-main.S4V3Bh3U/full-gate.zsh').read_text().replace('codex/iam-audit58-integration','codex/gw120-schema59-integration')
script=script.replace('date -u > "$arc_run/start"','export PATH="$(dirname "$arc_run")/offline-tools:$PATH"\ndate -u > "$arc_run/start"\n(cd web && pnpm install --offline --frozen-lockfile --ignore-scripts) > "$arc_run/dependency-bootstrap.log" 2>&1')
script=script.replace('make check > "$arc_run/check.log" 2>&1', 'bash "$(dirname "$arc_run")/mac-formal-focused.sh" "$arc_run/focused" "$arc_worktree" > "$arc_run/focused.log" 2>&1\nmake check > "$arc_run/check.log" 2>&1')
(run/'full-gate.zsh').write_text(script)
(base/'queued-mac-gate').write_text(str(run)+'\n')
deadline=time.monotonic()+1800;last=None
while True:
 try:
  lock.mkdir()
 except FileExistsError:
  try: owner=(lock/'owner').read_text().strip()
  except FileNotFoundError: owner='lock initialization'
  if owner!=last: print('Gate queued behind '+owner,flush=True);last=owner
 else:
  (lock/'owner').write_text(str(run)+'\n')
  if subprocess.run(['pgrep','-x','make|cargo|rustc|compile|link'],capture_output=True).returncode!=0:
   break
  (lock/'owner').unlink();lock.rmdir()
 if time.monotonic()>=deadline: raise SystemExit('Gate queue wait bounded at 30 minutes; no gate started')
 time.sleep(15)
(base/'latest-mac-gate').write_text(str(run)+'\n')
print('Acquired workstation gate: '+str(run),flush=True)
with (run/'runner.log').open('wb') as log:
 r=subprocess.run(['caffeinate','-i','zsh',str(run/'full-gate.zsh'),str(run),str(repo)],stdout=log,stderr=subprocess.STDOUT)
(run/'exit').write_text(str(r.returncode)+'\n')
print((run/'runner.log').read_text()[-2500:],flush=True)
print('Full gate exit '+str(r.returncode),flush=True)
raise SystemExit(r.returncode)
