import ast,hashlib,json,pathlib,shlex,subprocess,time
p=pathlib.Path(__file__).parent;script=(p/'stage_linux_2e64.py').read_text();ast.parse(script)
packet=p/'delivery-2e64ab4c-c5e4f1cb.tar.gz';assert hashlib.sha256(packet.read_bytes()).hexdigest()=='52a842955243ecfcf7efdf2afc8af87bd042ad4d0cd24095616121a451f0b468'
started=time.monotonic();capture=p/'linux-delivery-2e64ab4c.stdout.log';errors=p/'linux-delivery-2e64ab4c.stderr.log'
with packet.open('rb') as source,capture.open('x') as output,errors.open('x') as error:
 try:
  result=subprocess.run(['ssh','-T','-o','BatchMode=yes','-o','ConnectionAttempts=1','-o','ConnectTimeout=15','-o','ServerAliveInterval=10','-o','ServerAliveCountMax=2','ownsolid-cpu','python3 -c '+shlex.quote(script)],stdin=source,stdout=output,stderr=error,timeout=250)
  code=result.returncode
 except subprocess.TimeoutExpired:code=124
receipt=None
for line in capture.read_text().splitlines():
 if line.startswith('STAGE_RESULT_JSON '):
  message=json.loads(line[len('STAGE_RESULT_JSON '):]);receipt=message['receipt'];raw=json.dumps(receipt,indent=2)+'\n'
  if message['receipt_sha256'] is not None:assert hashlib.sha256(raw.encode()).hexdigest()==message['receipt_sha256']
  with (p/'linux-staging-result.json').open('x') as out:out.write(raw)
 else:print(line)
summary={'transport_exit':code,'elapsed_seconds':round(time.monotonic()-started,3),'remote_receipt_present':receipt is not None,'source_staged':receipt.get('worktree_staged',False) if receipt else None,'success':receipt.get('success',False) if receipt else None,'entry_executed':receipt.get('entry_executed') if receipt else None,'read_only_reconciliation_required':code!=0 or receipt is None or not receipt.get('success',False),'automatic_retry':False,'stderr_sha256':hashlib.sha256(errors.read_bytes()).hexdigest()}
with (p/'linux-delivery-summary.json').open('x') as out:json.dump(summary,out,indent=2);out.write('\n')
print('DELIVERY_SUMMARY',json.dumps(summary))
if receipt:print('STAGING_RESULT',json.dumps({key:receipt.get(key) for key in ['phase','error_phase','error_type','failure','source_head_after','source_tree_after','source_clean','canonical_entry_sha256','manifest_sha256','bundle_sha256','independent_bare_import','dag_verified','owned_paths_verified','projection_inputs_verified','sdk_archive_files_verified','protected_consumer_helpers_verified','retained_pointer_test_cases','retained_430_audit_exit','retained_old_source_ref','execution_state','entry_executed','locks_opened_or_acquired','git_failures']}))
