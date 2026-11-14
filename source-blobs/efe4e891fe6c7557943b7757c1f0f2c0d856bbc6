from pathlib import Path
import hashlib,json,os,shutil,stat,subprocess
work=Path(__file__).resolve().parent
identity=json.loads((work/'source-identity.json').read_text())
source=Path('/root/lane-arc-audit58.QQe1KRCn/repo'); target=work/'repo'
target.mkdir(mode=0o700)
for name,mode in json.loads((work/'base-paths.json').read_text()):
    src=source/name; dst=target/name; dst.parent.mkdir(parents=True,exist_ok=True)
    if mode=='120000': dst.symlink_to(os.readlink(src))
    else:
        shutil.copyfile(src,dst);dst.chmod(0o755 if mode=='100755' else 0o644)
def oid(kind,data):
    return hashlib.sha1(kind+b' '+str(len(data)).encode()+b'\0'+data).digest()
def tree(root):
    entries=[]
    for p in root.iterdir():
        if p.is_symlink(): mode=b'120000'; digest=oid(b'blob',os.readlink(p).encode());key=p.name.encode()
        elif p.is_dir(): mode=b'40000';digest=tree(p);key=p.name.encode()+b'/'
        elif p.is_file(): mode=b'100755' if p.stat().st_mode&0o111 else b'100644';digest=oid(b'blob',p.read_bytes());key=p.name.encode()
        else: raise RuntimeError('unexpected file '+str(p))
        entries.append((key,mode+b' '+p.name.encode()+b'\0'+digest))
    return oid(b'tree',b''.join(row for _,row in sorted(entries)))
assert tree(target).hex()==identity['base_tree'],'predecessor source tree mismatch'
patch=(work/'source.patch').read_bytes()
assert hashlib.sha256(patch).hexdigest()==identity['patch_sha256']
subprocess.run(['git','apply','--check',str(work/'source.patch')],cwd=target,check=True)
subprocess.run(['git','apply',str(work/'source.patch')],cwd=target,check=True)
actual=tree(target).hex();assert actual==identity['input_tree'],actual
(work/'verified-source-tree').write_text(actual+'\n')
print('Verified exact source tree',identity['input_commit'],actual,flush=True)
