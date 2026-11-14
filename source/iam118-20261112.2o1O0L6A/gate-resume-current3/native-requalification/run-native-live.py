import json, pathlib, subprocess, sys
p=pathlib.Path(sys.argv[2]); created=None
try:
    subprocess.run([sys.executable,str(p/'provision-provider.py'),str(p)],check=True,timeout=150)
    created=(p/'gitea-prototype/container-name').read_text().strip()
    with (p/'gitea-mounts.json').open('w') as out:
        subprocess.run(['docker','inspect',created,'--format','{{json .Mounts}}'],stdout=out,check=True,timeout=10)
    subprocess.run([str(p/'run-live.sh'),sys.argv[1],str(p)],check=True,timeout=240)
finally:
    if created:
        subprocess.run(['docker','rm','-f','-v',created],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=20)
