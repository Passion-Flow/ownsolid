import ast,base64,hashlib,json,pathlib,shlex,subprocess,time
p=pathlib.Path(__file__).parent;script=(p/'execute_authorized_2e64_remote.py').read_text();ast.parse(script)
output=p/'authorized-2e64-ssh.stdout.log';error=p/'authorized-2e64-ssh.stderr.log';started=time.monotonic()
with output.open('x') as out,error.open('x') as err:
 try:
  child=subprocess.run(['ssh','-T','-o','BatchMode=yes','-o','ConnectionAttempts=1','-o','ConnectTimeout=15','-o','ServerAliveInterval=10','-o','ServerAliveCountMax=2','ownsolid-cpu','python3 -c '+shlex.quote(script)],stdin=subprocess.DEVNULL,stdout=out,stderr=err,timeout=1270)
  code=child.returncode
 except subprocess.TimeoutExpired:code=124
receipt=None
for line in output.read_text().splitlines():
 if line.startswith('PRIVATE_AUTHORIZED_RESULT_JSON '):
  data=json.loads(line[len('PRIVATE_AUTHORIZED_RESULT_JSON '):]);receipt=data['receipt']
  for name,artifact in data['files'].items():
   content=base64.b64decode(artifact['content']);assert hashlib.sha256(content).hexdigest()==artifact['sha256']
   with (p/('actual-'+name)).open('xb') as out:out.write(content)
 else:print(line)
summary={'source':'2e64ab4c6c16bd84ae5c101b9de5444550760ff2','single_authorized_connection':True,'transport_exit':code,'elapsed_seconds':round(time.monotonic()-started,3),'receipt_received':receipt is not None,'receipt':receipt,'automatic_retry':False,'stderr_sha256':hashlib.sha256(error.read_bytes()).hexdigest()}
with (p/'authorized-2e64-attempt-summary.json').open('x') as out:json.dump(summary,out,indent=2);out.write('\n')
print('AUTHORIZED_ATTEMPT_SUMMARY '+json.dumps(summary))
