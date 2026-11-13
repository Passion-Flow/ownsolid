"""Publish only this lane's private read-only review artifacts to its test host."""
import base64,hashlib,json,os,shlex,signal,subprocess
from pathlib import Path
root=Path(__file__).resolve().parent
files={name:base64.b64encode((root/name).read_bytes()).decode() for name in ['contract-review.md','review.json','mac-unit-proof.json','private-http-unit-proof.json']}
code='''import base64,hashlib,json,os,sys
from pathlib import Path
os.umask(0o077)
root=Path("/root/lane-rel-tools/obs110-review-3cebfcef");assert root.is_dir() and not root.is_symlink() and root.stat().st_uid==os.geteuid()
for name,data in json.loads(sys.argv[1]).items():
 assert name in ["contract-review.md","review.json","mac-unit-proof.json","private-http-unit-proof.json"]
 p=root/name;assert not p.is_symlink();p.write_bytes(base64.b64decode(data,validate=True))
r=json.loads((root/"review.json").read_text());assert hashlib.sha256((root/"contract-review.md").read_bytes()).hexdigest()==r["review_sha256"]
assert hashlib.sha256((root/"mac-unit-proof.json").read_bytes()).hexdigest()==r["mac_unit_evidence"]["receipt_sha256"]
print(json.dumps({"review_sha256":r["review_sha256"],"mac_unit_evidence_verified":True,"public_changes":False}))'''
p=subprocess.Popen(['ssh','-T','-S','none','-o','BatchMode=yes','-o','ConnectTimeout=20','-o','ServerAliveInterval=30','-o','ServerAliveCountMax=3','ownsolid-cpu',shlex.join(['python3','-c',code,json.dumps(files,separators=(',',':'))])],stdout=subprocess.PIPE,stderr=subprocess.PIPE,start_new_session=True)
try: out,err=p.communicate(timeout=55)
except subprocess.TimeoutExpired:
 os.killpg(p.pid,signal.SIGTERM);out,err=p.communicate(timeout=5)
(root/'delivery-result.json').write_text(json.dumps({'exit':p.returncode,'stdout':out.decode(),'stderr':err.decode()},indent=2)+'\n')
print(out.decode());print('delivery_exit',p.returncode)
