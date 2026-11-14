"""Generate site-owned private RKE2 policy and ephemeral Registry identity."""
import json
import os
from pathlib import Path
import secrets
import subprocess
import sys

root = Path(sys.argv[1])
address = sys.argv[2]
authority = f"{address}:5443"
os.umask(0o077)
(root / "worker/config.yaml.d").mkdir(parents=True)
def create_ca(name, subject):
    subprocess.run(["openssl", "req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1",
                    "-subj", f"/CN={subject}", "-addext", "basicConstraints=critical,CA:TRUE",
                    "-keyout", str(root/f"{name}.key"), "-out", str(root/f"{name}.pem")],
                   check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
create_ca("site-ca", "ARC private test site")
create_ca("wrong-ca", "ARC untrusted test root")
(root/"site.ext").write_text(f"subjectAltName=IP:{address}\nextendedKeyUsage=serverAuth\n")
subprocess.run(["openssl", "req", "-new", "-newkey", "rsa:2048", "-nodes",
                "-subj", f"/CN={address}", "-keyout", str(root/"site.key"), "-out", str(root/"site.csr")],
               check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
subprocess.run(["openssl", "x509", "-req", "-in", str(root/"site.csr"), "-CA", str(root/"site-ca.pem"),
                "-CAkey", str(root/"site-ca.key"), "-CAcreateserial", "-days", "1",
                "-extfile", str(root/"site.ext"), "-out", str(root/"site.pem")],
               check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
for name in ("site-ca.key", "wrong-ca.key", "site.csr", "site.ext", "site-ca.srl"):
    (root/name).unlink()
name = "arc-site-worker"
password = secrets.token_urlsafe(32)
(root/"credential.json").write_text(json.dumps({"username": name, "password": password}))
(root/"site.yaml").write_text(f"""root: /site-runtime/registry
name: arc104-site-registry
listen: {authority}
uid: 18080
gid: 18080
request_seconds: 120
tls_ca_file: /site-inputs/site-ca.pem
tls_certificate_file: /site-inputs/site.pem
tls_key_file: /site-inputs/site.key
credential_file: /site-inputs/credential.json
""")
(root/"verification.json").write_text(json.dumps({"mount_root": "/release",
    "trust_file": "/release/registry-stage/release-trust.json", "minimum_free_bytes": 1048576}))
native = f'''mirrors:
  "docker.io":
    endpoint:
      - "https://{authority}"
configs:
  "{authority}":
    tls:
      ca_file: "/etc/rancher/rke2/registry-ca.pem"
'''
(root/"anonymous.yaml").write_text(native)
(root/"worker/registries.yaml").write_text(native+f'''    auth:
      username: {json.dumps(name)}
      password: {json.dumps(password)}
''')
(root/"worker/registry-ca.pem").write_bytes((root/"site-ca.pem").read_bytes())
(root/"worker/config.yaml.d/99-site-offline.yaml").write_text("disable-default-registry-endpoint: true\n")
