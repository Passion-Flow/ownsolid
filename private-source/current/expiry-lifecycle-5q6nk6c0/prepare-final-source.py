import gzip
import hashlib
import json
import pathlib
import re
import subprocess
import sys

worktree = pathlib.Path(sys.argv[1])
proof = pathlib.Path(__file__).resolve().parent

def git(*args):
    return subprocess.check_output(['git', *args], cwd=worktree)

if git('status', '--porcelain').strip():
    raise SystemExit('Expected a clean owned worktree')
raw = subprocess.check_output(['scutil', '--proxy'], text=True)
settings = dict(re.findall(r'^\s*(HTTPSEnable|HTTPSProxy|HTTPSPort|HTTPEnable|HTTPProxy|HTTPPort)\s*:\s*(.*?)\s*$', raw, re.M))
args = ['git', '-c', 'http.version=HTTP/1.1']
for kind in ('HTTPS', 'HTTP'):
    if settings.get(kind + 'Enable') == '1' and settings.get(kind + 'Proxy') in ('127.0.0.1', 'localhost'):
        args += ['-c', 'http.proxy=http://' + settings[kind + 'Proxy'] + ':' + str(int(settings[kind + 'Port']))]
        break
subprocess.run(args + ['fetch', 'origin'], cwd=worktree, check=True, timeout=45)
upstream = git('rev-parse', 'origin/main').decode().strip()
subprocess.run(['git', 'rebase', upstream], cwd=worktree, check=True, timeout=45)
paths = ['services/control/internal/modules/models', 'services/control/internal/modules/modelpreparations', 'services/control/internal/modules/deployments', 'runtimes/model-lifecycle']
subprocess.run(['git', 'diff', '--exit-code', 'e7198781', 'HEAD', '--', *paths], cwd=worktree, check=True)
subprocess.run(['git', 'diff', '--check', upstream + '...HEAD'], cwd=worktree, check=True)
python = subprocess.check_output(['uv', 'python', 'find', '3.12'], text=True).strip()
subprocess.run([python, 'tools/dev/roadmap_progress.py'], cwd=worktree, check=True, timeout=45)
if git('status', '--porcelain').strip():
    raise SystemExit('Unexpected roadmap regeneration change')
subprocess.run(['make', 'check-layout', 'check-roadmap', 'PYTHON=' + python], cwd=worktree, check=True, timeout=45)
old = json.loads((proof / 'source-delta.json').read_text())
target = git('rev-parse', 'HEAD').decode().strip()
delta = gzip.compress(git('diff', '--binary', old['base_commit'], target), mtime=0)
(proof / 'source-delta-schema59.patch.gz').write_bytes(delta)
spec = dict(old, target_commit=target, target_baseline=upstream, delta_sha256=hashlib.sha256(delta).hexdigest(), native_gate_source='e71987812e10a55fc0018cfcb79082653e2de778')
(proof / 'source-delta-schema59.json').write_text(json.dumps(spec, indent=2) + '\n')
print('SOURCE_READY', target, 'baseline', upstream, 'compressed_delta_bytes', len(delta))
