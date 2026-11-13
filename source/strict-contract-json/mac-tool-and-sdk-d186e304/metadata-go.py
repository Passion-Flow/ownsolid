#!/Users/passion./.local/share/uv/python/cpython-3.12.14-macos-aarch64-none/bin/python3.12
import hashlib,json,os,sys,time
from pathlib import Path
r=Path(__file__).resolve().parents[1]
c=json.loads((r/'manifest.json').read_text())
a=sys.argv[1:]
allowed=[['env', '-json', 'GO111MODULE', 'GOFLAGS', 'GOINSECURE', 'GOMOD', 'GOMODCACHE', 'GONOPROXY', 'GONOSUMDB', 'GOPATH', 'GOPROXY', 'GOROOT', 'GOSUMDB', 'GOWORK'], ['list', '-m', '-e', '-json', '...']]
if a not in allowed:raise SystemExit('UNEXPECTED_GO_METADATA_COMMAND')
p=Path(c['go']['path'])
if hashlib.sha256(p.read_bytes()).hexdigest()!=c['go']['sha256']:raise SystemExit('GO_METADATA_BINARY_CHANGED')
fd=os.open(r/'go-metadata.jsonl',os.O_WRONLY|os.O_APPEND|os.O_CREAT,0o600)
os.write(fd,(json.dumps({'argv':a,'executable_sha256':c['go']['sha256']})+'\n').encode());os.close(fd)
os.execv(str(p),[str(p),*a])
