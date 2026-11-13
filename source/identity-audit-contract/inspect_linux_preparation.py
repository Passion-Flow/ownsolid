"""Inspect the staged source and offline caches without invoking a gate."""

from pathlib import Path
import datetime
import hashlib
import json
import os
import shutil
import subprocess

WORK = Path('/root/lane-rel-identity-audit-contract')
ROOT = Path('/root/lane-rel-tools/identity-audit-contract')
SOURCE = '9be01cbbf0144a2f2537667834539de31e40f58f'
TREE = 'b8aea641ecd1b79be71270ad80ab03c1e131df31'


def digest(path):
    with Path(path).open('rb') as handle:
        return hashlib.file_digest(handle, 'sha256').hexdigest()


assert subprocess.check_output(['git', 'rev-parse', 'HEAD'], cwd=WORK, text=True).strip() == SOURCE
assert subprocess.check_output(['git', 'rev-parse', 'HEAD^{tree}'], cwd=WORK, text=True).strip() == TREE
proof = {
    'source': SOURCE, 'tree': TREE,
    'observed_utc': datetime.datetime.now(datetime.timezone.utc).isoformat(),
    'gate_invoked': False, 'admission_attempted': False, 'queue_created': False,
    'build_test_or_browser_started': False, 'tools': {}, 'cache_checks': {},
    'preparation_failures_retained': [
        'Direct Popen(pnpm) returned errno8 for the official shebangless placeholder.',
        'Two later SSH diagnostics failed before remote execution; a third read succeeded.',
        'An inline metadata collector had a quoting SyntaxError before execution; this file replaces it.',
    ],
    'cache_paths': {name: os.environ.get(name) for name in (
        'GOMODCACHE', 'GOCACHE', 'CARGO_HOME', 'CARGO_TARGET_DIR',
        'GOWORK', 'PLAYWRIGHT_BROWSERS_PATH',
    )},
}
environment = {
    **os.environ, 'GOTOOLCHAIN': 'local', 'GOPROXY': 'off', 'GOSUMDB': 'off',
    'GOTELEMETRY': 'off', 'GOMAXPROCS': '2', 'GOFLAGS': '-p=1 -mod=readonly',
    'CARGO_NET_OFFLINE': 'true', 'CARGO_BUILD_JOBS': '1', 'RUSTUP_AUTO_INSTALL': '0',
    'PNPM_CONFIG_OFFLINE': 'true', 'PNPM_CONFIG_UPDATE_NOTIFIER': 'false',
    'NODE_OPTIONS': '--max-old-space-size=768', 'PYTHONDONTWRITEBYTECODE': '1',
}


def save():
    (ROOT / 'linux-source-toolchain-preparation.json').write_text(json.dumps(proof, indent=2) + '\n')


def run(name, arguments, cwd=WORK, seconds=35, extra_environment=None):
    try:
        result = subprocess.run(
            arguments, cwd=cwd, env={**environment, **(extra_environment or {})},
            text=True, capture_output=True, timeout=seconds,
        )
        record = {'argv': arguments, 'exit': result.returncode,
                  'stdout': result.stdout, 'stderr': result.stderr}
    except (OSError, subprocess.TimeoutExpired) as error:
        record = {'argv': arguments, 'exit': None, 'error': type(error).__name__ + ': ' + str(error)}
    print(name, record.get('exit'), flush=True)
    return record


for name, arguments in [
    ('go', ['go', 'version']), ('cargo', ['cargo', '--version']),
    ('rustc', ['rustc', '--version']), ('node', ['node', '--version']),
    ('python3', ['python3', '--version']), ('gcc', ['gcc', '--version']),
    ('unshare', ['unshare', '--version']), ('ip', ['ip', '-Version']),
]:
    record = run(name, arguments)
    path = shutil.which(arguments[0])
    record['path'] = path
    if path:
        record['entry_sha256'] = digest(path)
    proof['tools'][name] = record
    save()

pnpm = Path('/root/lane-rel-tools/node/bin/pnpm')
native = Path('/root/lane-rel-tools/node/lib/node_modules/pnpm/node_modules/@pnpm/exe.linux-x64/pnpm')
with native.open('rb') as handle:
    header = handle.read(24)
assert header[:4] == b'\x7fELF' and int.from_bytes(header[18:20], 'little') == 62
proof['tools']['pnpm'] = {
    **run('pnpm shell entry', ['/bin/sh', '-c', 'exec /root/lane-rel-tools/node/bin/pnpm --version']),
    'path': str(pnpm), 'entry_kind': 'Official shebangless shell placeholder',
    'entry_sha256': digest(pnpm), 'native_path': str(native),
    'native_sha256': digest(native), 'native_architecture': 'ELF64 x86_64',
    'binary_replaced': False,
}
proof['cache_checks']['effective_go_environment'] = run(
    'go environment', ['go', 'env', 'GOWORK', 'GOMODCACHE', 'GOCACHE', 'GOTOOLCHAIN'])
for name in ['cargo', 'rustc']:
    record = run('rustup which ' + name, ['rustup', 'which', name])
    path = record.get('stdout', '').strip()
    if record.get('exit') == 0 and Path(path).is_file():
        proof['tools'][name]['resolved_binary'] = {'path': path, 'sha256': digest(path)}

memory = {line.split(':', 1)[0]: int(line.split()[1])
          for line in Path('/proc/meminfo').read_text().splitlines()
          if line.startswith(('MemFree:', 'MemAvailable:'))}
disk = os.statvfs('/root')
pressure = next(line for line in Path('/proc/pressure/io').read_text().splitlines()
                if line.startswith('full '))
io_average = float(pressure.split('avg10=', 1)[1].split()[0])
proof['resource_snapshot'] = {
    'memfree_mib': memory['MemFree'] // 1024,
    'memavailable_mib': memory['MemAvailable'] // 1024,
    'disk_free_bytes': disk.f_bavail * disk.f_frsize, 'io_full_avg10': io_average,
}
profile = (memory['MemFree'] >= 2048 * 1024 and memory['MemAvailable'] >= 8192 * 1024
           and proof['resource_snapshot']['disk_free_bytes'] >= 30 * 1024**3
           and io_average <= 5)
proof['cache_preparation_numeric_profile_met'] = profile
save()
if profile:
    record = run('offline Go module graph', ['go', 'list', '-m', '-json', 'all'], seconds=40,
                 extra_environment={'GOWORK': str(WORK / 'go.work')})
    (ROOT / 'linux-go-module-graph.json').write_text(record.pop('stdout', ''))
    proof['cache_checks']['go_module_graph'] = record
    record = run('offline Cargo metadata', [
        'cargo', 'metadata', '--locked', '--offline', '--format-version=1',
        '--filter-platform=x86_64-unknown-linux-gnu',
    ], cwd=WORK / 'services/gateway', seconds=40)
    (ROOT / 'linux-cargo-metadata.json').write_text(record.pop('stdout', ''))
    proof['cache_checks']['cargo_metadata'] = record
    save()
    record = run('offline web dependencies', [
        '/bin/sh', '-c',
        'exec /root/lane-rel-tools/node/bin/pnpm install --offline --frozen-lockfile --ignore-scripts',
    ], cwd=WORK / 'web', seconds=45)
    (ROOT / 'linux-web-cache-preparation.log').write_text(
        record.get('stdout', '') + record.get('stderr', ''))
    proof['cache_checks']['web_dependencies'] = record
    save()
else:
    proof['cache_checks']['module_and_web_preparation'] = {
        'run': False, 'reason': 'Strict numeric profile not met; no wait or admission attempted',
    }

browser = {'browser_started': False, 'metadata_manifests': [], 'executable_candidates': []}
for manifest in (WORK / 'web/node_modules/.pnpm').glob('playwright-core@*/node_modules/playwright-core/browsers.json'):
    browser['metadata_manifests'].append(str(manifest))
    cache_setting = os.environ.get('PLAYWRIGHT_BROWSERS_PATH')
    cache = (manifest.parent / '.local-browsers' if cache_setting == '0'
             else Path(cache_setting or str(Path.home() / '.cache/ms-playwright')))
    browser['cache_root'] = str(cache)
    for item in json.loads(manifest.read_text())['browsers']:
        if item['name'] not in ['chromium', 'chromium-headless-shell']:
            continue
        folder = cache / (item['name'].replace('-', '_') + '-' + item['revision'])
        for relative in ['chrome-linux64/chrome', 'chrome-linux/chrome',
                         'chrome-headless-shell-linux64/chrome-headless-shell', 'chrome-linux/headless_shell']:
            executable = folder / relative
            if not executable.is_file():
                continue
            record = run('browser library metadata', ['ldd', str(executable)], seconds=10)
            browser['executable_candidates'].append({
                'path': str(executable), 'sha256': digest(executable),
                'ldd_exit': record.get('exit'),
                'missing_libraries': [line.strip() for line in record.get('stdout', '').splitlines()
                                      if 'not found' in line],
            })
proof['cache_checks']['playwright_browser'] = browser
proof['source_clean_after'] = not bool(subprocess.check_output(
    ['git', 'status', '--porcelain'], cwd=WORK, text=True).strip())
proof['finished_utc'] = datetime.datetime.now(datetime.timezone.utc).isoformat()
save()
print(json.dumps(proof, indent=2), flush=True)
