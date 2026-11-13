import datetime,hashlib,json,subprocess
from pathlib import Path
root=Path('/root/lane-rel-tools/user-reads/native-client-410ed356');target=root/'ownsolid'
admission=json.loads(Path('/root/lane-rel-tools/user-reads/native-build-admission-410ed356.json').read_text())
assert admission['exit']==0 and admission['build_exit']==0 and admission['source']=='410ed356ffaf9c5a54a32d9b19e20ea457090f7b'
sha=hashlib.sha256(target.read_bytes()).hexdigest();assert sha==admission['binary_sha256']=='e630d9ff65172d6e127a7aa055a5979d2f434abe13eb085cfc1d0ef5d3ece257'
description=subprocess.check_output(['file',str(target)],text=True).strip();assert 'statically linked' in description and 'x86-64' in description
receipt={'source':admission['source'],'binary_sha256':sha,'binary_bytes':target.stat().st_size,'built_and_verified':True,'native_build':True,'installed_acceptance':False,'file':description,'utc':datetime.datetime.now(datetime.timezone.utc).isoformat()}
(root/'receipt.json').write_text(json.dumps(receipt,indent=2)+'\n')

"""Qualify native startup of the received exact CLI without any server session."""
import datetime
import hashlib
import json
import os
from pathlib import Path
import stat
import subprocess
import tempfile

os.umask(0o077)
root = Path('/root/lane-rel-tools/user-reads/native-client-410ed356')
target = root / 'ownsolid'
expected = 'e630d9ff65172d6e127a7aa055a5979d2f434abe13eb085cfc1d0ef5d3ece257'
source = '410ed356ffaf9c5a54a32d9b19e20ea457090f7b'
info = target.lstat()
assert stat.S_ISREG(info.st_mode) and info.st_uid == os.geteuid() and info.st_size == 12101750
assert not info.st_mode & 0o077 and hashlib.sha256(target.read_bytes()).hexdigest() == expected
receipt = json.loads((root / 'receipt.json').read_text())
assert receipt['built_and_verified'] is True and receipt['source'] == source
assert receipt['binary_sha256'] == expected and receipt['installed_acceptance'] is False
inner = r'''
import hashlib,json,os,subprocess,sys
from pathlib import Path
root=Path(sys.argv[1]); binary=root/'ownsolid'; source=sys.argv[2]; evidence=[]
interfaces=sorted(line.split(':',1)[0].strip() for line in Path('/proc/net/dev').read_text().splitlines()[2:] if ':' in line)
assert os.stat('/proc/self/ns/net').st_ino != int(sys.argv[4])
assert interfaces==['lo'],interfaces
assert not Path('/proc/net/route').read_text().splitlines()[1:]
env={'PATH':'/usr/bin:/bin','LANG':'C.UTF-8','LC_ALL':'C.UTF-8',
     'HOME':sys.argv[3],'OWNSOLID_CONFIG_DIR':sys.argv[3]+'/config'}
cases=[
 (['--help'],0,['user','workspace','session']),
 (['user','list','--help'],0,['--email-prefix','--query','--status','--cursor']),
 (['user','get','--help'],0,['<user-id>']),
 (['workspace','member','list','--help'],0,['<workspace-id>','--cursor']),
 (['session','revoke','--help'],0,['--confirm']),
 (['version','--output','json'],0,[]),
 (['user','get','00000000-0000-0000-0000-000000000000'],1,['canonical lowercase nonzero UUID']),
 (['user','list','--status','unknown'],1,['--status must be active or disabled']),
 (['user','list','--query='],1,['--query must contain']),
 (['user','list','--limit','0'],1,['--limit must be']),
 (['--server','https://127.0.0.1:1','user','list'],1,['no saved Admin session'])]
for args,code,needles in cases:
 result=subprocess.run([str(binary),*args],env=env,capture_output=True,text=True,timeout=15)
 assert result.returncode==code,(args,result.returncode,result.stderr)
 text=result.stdout if code==0 else result.stderr
 assert all(needle in text for needle in needles),(args,text)
 if code!=0: assert not result.stdout,(args,'unexpected success output')
 if args==['version','--output','json']:
  version=json.loads(result.stdout);assert version['revision']==source,version
 evidence.append({'args':args,'exit':result.returncode,'stdout':result.stdout,'stderr':result.stderr})
print(json.dumps({'network_namespace_interfaces':interfaces,'route_count':0,'cases':evidence}))
'''
for name in ('native-startup.stdout','native-startup.stderr'):
    current=root/name; archived=root/name.replace('native-startup','native-startup-sysfs-failure')
    if current.exists() and not archived.exists():
        os.rename(current,archived)
with tempfile.TemporaryDirectory(prefix='startup-', dir=root) as scratch:
    os.chmod(scratch, 0o700)
    result = subprocess.run(['unshare', '--net', '--', 'python3', '-c', inner,
                             str(root), source, scratch, str(os.stat('/proc/self/ns/net').st_ino)], capture_output=True, text=True, timeout=180)
    (root / 'native-startup.stdout').write_text(result.stdout)
    (root / 'native-startup.stderr').write_text(result.stderr)
    assert result.returncode == 0, result.stderr
    evidence = json.loads(result.stdout)
proof = {'format_version': 1, 'source': source, 'binary_sha256': expected,
         'binary_path': str(target), 'host_platform': 'linux/amd64',
         'native_linux_startup_run': True, 'offline_namespace': True,
         'cases_passed': len(evidence['cases']), 'exit': 0,
         'installed_acceptance': False,
         'boundary': 'Version/help, local validation and absent-credential refusal only; no authenticated protocol flow',
         'utc': datetime.datetime.now(datetime.timezone.utc).isoformat()}
(root / 'native-startup.json').write_text(json.dumps(proof, indent=2) + '\n')
print(json.dumps(proof))
