import json, os, pathlib, re, subprocess, sys, time, uuid
os.umask(0o077)
d = pathlib.Path(sys.argv[1]) / 'gitea-prototype'
d.mkdir(exist_ok=True)
name = 'iam118-gitea-' + uuid.uuid4().hex[:10]
(d / 'container-name').write_text(name + '\n')
image = 'gitea/gitea@sha256:b1a90409ae9afcd5a5725404c3639da7245248848b99e35332968891290b953e'
config = '''APP_NAME = IAM isolated provider fixture
RUN_USER = git
RUN_MODE = prod
WORK_PATH = /tmp/iam
[server]
PROTOCOL = https
HTTP_ADDR = 127.0.0.1
HTTP_PORT = 18445
ROOT_URL = https://127.0.0.1:18445/
DOMAIN = 127.0.0.1
CERT_FILE = /tmp/iam/server.pem
KEY_FILE = /tmp/iam/server-key.pem
DISABLE_SSH = true
OFFLINE_MODE = true
[database]
DB_TYPE = sqlite3
PATH = /tmp/iam/gitea.db
[security]
INSTALL_LOCK = true
[service]
DISABLE_REGISTRATION = true
REQUIRE_SIGNIN_VIEW = false
[mailer]
ENABLED = false
[openid]
ENABLE_OPENID_SIGNIN = false
ENABLE_OPENID_SIGNUP = false
[oauth2]
ENABLED = true
[log]
MODE = console
LEVEL = Warn
'''
(d / 'app.ini').write_text(config)
def run(args, log, data=None):
    with (d / log).open('wb') as output:
        result = subprocess.run(args, input=data, stdout=output, stderr=subprocess.STDOUT, timeout=45)
    if result.returncode:
        raise RuntimeError('provider fixture step failed: ' + log)
try:
    run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-days','1','-keyout',str(d/'server-key.pem'),'-out',str(d/'server.pem'),'-subj','/CN=127.0.0.1','-addext','subjectAltName=IP:127.0.0.1'],'certificate.log')
    run(['docker','run','-d','--rm','--pull','never','--platform','linux/amd64','--network','none','--name',name,'--tmpfs','/tmp/iam:rw,uid=1000,gid=1000,mode=0700','--entrypoint','/bin/sh',image,'-c','exec sleep infinity'],'container.log')
    for file in ['app.ini','server.pem','server-key.pem']:
        run(['docker','exec','-i','--user','1000',name,'/bin/sh','-c','umask 077; cat > /tmp/iam/'+file],'stream-'+file+'.log',(d/file).read_bytes())
    command = ['docker','exec','--user','1000',name,'/usr/local/bin/gitea']
    flags = ['--work-path','/tmp/iam','--config','/tmp/iam/app.ini']
    run(command+['migrate']+flags,'migrate.log')
    run(command+['admin','user','create']+flags+['--username','iam-fixture','--email','iam-fixture@example.invalid','--random-password','--random-password-length','32','--must-change-password=false'],'private-user-create.log')
    match = re.search(r"generated random password is '([^']+)'", (d/'private-user-create.log').read_text())
    if not match:
        raise RuntimeError('protected random-password receipt invalid')
    (d/'user-password').write_text(match[1]+'\n')
    (d/'fixture.json').write_text(json.dumps({'origin':'https://127.0.0.1:18445','ca_file':'/tmp/iam-live/server.pem','username':'iam-fixture','password_file':'/tmp/iam-live/user-password'}))
    run(['docker','exec','--detach','--user','1000',name,'/bin/sh','-c','exec /usr/local/bin/gitea web --work-path /tmp/iam --config /tmp/iam/app.ini > /tmp/iam/server.log 2>&1'],'start.log')
    for attempt in range(20):
        probe = subprocess.run(['docker','exec','--user','1000',name,'curl','--fail','--silent','--show-error','--cacert','/tmp/iam/server.pem','https://127.0.0.1:18445/api/v1/version'],capture_output=True,timeout=5)
        if not probe.returncode and json.loads(probe.stdout).get('version') == '28.1.0':
            break
        time.sleep(1)
    else:
        raise RuntimeError('verified pinned provider startup failed')
    print('isolated pinned provider ready over verified TLS')
except Exception:
    subprocess.run(['docker','rm','-f','-v',name],stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL,timeout=15)
    raise
