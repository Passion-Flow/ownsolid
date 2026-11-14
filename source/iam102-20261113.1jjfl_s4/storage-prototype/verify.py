import hashlib,json,os,pathlib,re,select,subprocess,sys,tempfile,time,uuid
os.umask(0o077)
root=pathlib.Path(__file__).parent;run=pathlib.Path(tempfile.mkdtemp(prefix='run-',dir=root));(root/'latest-run-path').write_text(str(run)+'\n');records=[];container=None
image_info=json.loads(subprocess.check_output(['docker','image','inspect','pgvector/pgvector:0.8.7-pg18-bookworm']))[0];assert image_info['Architecture']=='arm64';image=image_info['Id']
def execute(cmd,name,stdin=None,timeout=30,allow_failure=False):
 r=subprocess.run(cmd,input=stdin,text=True,capture_output=True,timeout=timeout);(run/(name+'.log')).write_text(r.stdout+r.stderr)
 if r.returncode and not allow_failure:raise RuntimeError(name+' failed; raw log retained')
 return r
def sql(source,name,code=None):
 r=execute(['docker','exec','-i','--user','postgres',container,'psql','-X','-A','-t','-q','-1','-v','ON_ERROR_STOP=1','-v','VERBOSITY=verbose','-h','/tmp/oidc-socket','-d','postgres'],name,source,allow_failure=code is not None)
 found=re.search(r'ERROR:\s+([A-Z0-9]{5}):',r.stderr);actual=found.group(1) if found else None
 if code and (r.returncode==0 or actual not in code):raise RuntimeError(name+' did not enforce expected SQLSTATE '+str(code))
 records.append({'name':name,'exit_code':r.returncode,'expected_sqlstate':code,'observed_sqlstate':actual});(run/'results.json').write_text(json.dumps(records,indent=2)+'\n');print(name,actual or 'ok',flush=True)
 return r.stdout.strip()
def uid():return str(uuid.uuid4())
def literal(value):return "'"+value.replace("'","''")+"'"
def blob(n):return "decode('"+os.urandom(n).hex()+"','hex')"
def connector(tenant,user,provider,client,issuer=None,identifier=None):
 identifier=identifier or uid();return identifier,"INSERT INTO sso_connectors(id,tenant_id,name,provider,origin,client_id,secret_key_id,secret_nonce,secret_ciphertext,enabled,created_by"+(',issuer' if issuer is not None else '')+") VALUES("+','.join([literal(identifier),literal(tenant),"'Synthetic connector'",literal(provider),"'https://provider.oidc.test'",literal(client),"'synthetic-key'",blob(12),blob(59),'true',literal(user)]+([literal(issuer)] if issuer is not None else []))+');'
def binding(tenant,connector_id,user,subject,provider=None):
 identifier=uid();return identifier,'INSERT INTO sso_bindings(id,tenant_id,connector_id,user_id,subject'+(',provider' if provider else '')+') VALUES('+','.join([literal(identifier),literal(tenant),literal(connector_id),literal(user),literal(subject)]+([literal(provider)] if provider else []))+');'
def flow(tenant,connector_id,provider=None,nonce=None,state_hash=None):
 identifier=uid();fields=['id','tenant_id','connector_id','connector_revision','auth_version','audience','mode','state_hash','browser_hash','previous_cookie_hash','verifier_key_id','verifier_nonce','verifier_ciphertext','expires_at'];values=[literal(identifier),literal(tenant),literal(connector_id),'1','1',"'portal'","'login'",state_hash or blob(32),blob(32),blob(32),"'synthetic-key'",blob(12),blob(59),"clock_timestamp()+interval '5 minutes'"]
 if provider:fields+=['provider'];values+=[literal(provider)]
 if nonce is not None:fields+=['oidc_nonce_hash'];values+=[nonce]
 return identifier,'INSERT INTO sso_flows('+','.join(fields)+') VALUES('+','.join(values)+');'
try:
 container=execute(['docker','create','--network','none','--memory','512m','--cpus','1','--pids-limit','128','--user','postgres','--entrypoint','/bin/sh',image,'-c','set -eu; mkdir -p /tmp/oidc-data /tmp/oidc-socket; chmod 700 /tmp/oidc-data /tmp/oidc-socket; initdb -D /tmp/oidc-data --auth-local=peer --auth-host=reject >/tmp/oidc-init.log; exec postgres -D /tmp/oidc-data -k /tmp/oidc-socket -c listen_addresses= -c max_connections=12 -c shared_buffers=32MB'],'create').stdout.strip();assert len(container)==64
 (run/'container-id').write_text(container+'\n');execute(['docker','start',container],'start')
 ready=False
 for attempt in range(60):
  r=execute(['docker','exec','--user','postgres',container,'pg_isready','-h','/tmp/oidc-socket'],'ready-'+str(attempt),allow_failure=True)
  if r.returncode==0:ready=True;break
  time.sleep(.1)
 if not ready:raise RuntimeError('owned PostgreSQL startup exceeded its bound')
 sql("""CREATE TABLE tenants(id uuid PRIMARY KEY);CREATE TABLE users(id uuid PRIMARY KEY);
 CREATE TABLE sessions(id uuid PRIMARY KEY,user_id uuid NOT NULL REFERENCES users(id),revoked_at timestamptz,sso_tenant_id uuid,sso_connector_id uuid,sso_binding_id uuid,sso_auth_version bigint,sso_flow_id uuid,
 CONSTRAINT account_local_session_only CHECK(sso_tenant_id IS NULL AND sso_connector_id IS NULL AND sso_binding_id IS NULL AND sso_auth_version IS NULL AND sso_flow_id IS NULL));""",'legacy-base')
 sql((root/'legacy-sso.sql').read_text(),'legacy-sso')
 tenant,foreign,user=uid(),uid(),uid();sql('INSERT INTO tenants VALUES('+literal(tenant)+'),('+literal(foreign)+');INSERT INTO users VALUES('+literal(user)+');','seed-authority')
 old,statement=connector(tenant,user,'gitea',uid());sql(statement,'seed-legacy-connector');old_binding,statement=binding(tenant,old,user,'9223372036854775807');sql(statement,'seed-legacy-binding');old_flow,statement=flow(tenant,old);sql(statement,'seed-legacy-flow')
 intent=uid();sql('INSERT INTO sso_mutation_intents(tenant_id,actor_user_id,idempotency_key,operation,target_id,fingerprint,result) VALUES('+','.join([literal(tenant),literal(user),literal(intent),"'create_connector'",literal(old),blob(32),"'{\"provider\":\"gitea\"}'::jsonb"])+');','seed-retained-intent')
 snapshot="SELECT jsonb_build_object('connector',(SELECT to_jsonb(c)-'issuer' FROM sso_connectors c WHERE id="+literal(old)+"),'binding',(SELECT to_jsonb(b)-'provider' FROM sso_bindings b WHERE id="+literal(old_binding)+"),'flow',(SELECT to_jsonb(f)-'provider'-'oidc_nonce_hash' FROM sso_flows f WHERE id="+literal(old_flow)+"),'intent',(SELECT to_jsonb(i) FROM sso_mutation_intents i WHERE idempotency_key="+literal(intent)+"))::text;"
 before=sql(snapshot,'legacy-before');tables=sql("SELECT count(*) FROM pg_tables WHERE schemaname='public';",'table-count-before')
 sql((root/'profile.up.sql').read_text(),'upgrade-legacy');assert sql(snapshot,'legacy-after-upgrade')==before;assert sql("SELECT count(*) FROM pg_tables WHERE schemaname='public';",'table-count-after')==tables
 sql((root/'profile.down.sql').read_text(),'downgrade-legacy');assert sql(snapshot,'legacy-after-downgrade')==before
 sql((root/'profile.up.sql').read_text(),'upgrade-for-oidc')
 oidc,statement=connector(tenant,user,'oidc','opaque:client+identifier','https://provider.oidc.test/realms/private');sql(statement,'oidc-connector')
 bindings=[]
 for i,subject in enumerate(['Opaque:Subject_001','opaque:Subject_001','00001','922337203685477580700','x'*255]):
  identifier,statement=binding(tenant,oidc,user,subject,'oidc');sql(statement,'opaque-subject-'+str(i));bindings.append(identifier)
 assert sql('SELECT count(*) FROM sso_bindings WHERE connector_id='+literal(oidc)+';','opaque-distinct-count')=='5'
 for name,statement,codes in [
  ('oidc-missing-issuer',connector(tenant,user,'oidc','client')[1],['23514']),
  ('gitea-with-issuer',connector(tenant,user,'gitea',uid(),'https://provider.oidc.test/realm')[1],['23514']),
  ('gitea-invalid-client',connector(tenant,user,'gitea','opaque-client')[1],['23514']),
  ('oidc-empty-client',connector(tenant,user,'oidc','','https://provider.oidc.test/realm')[1],['23514']),
  ('oidc-space-client',connector(tenant,user,'oidc','   ','https://provider.oidc.test/realm')[1],['23514']),
  ('oidc-nonascii-client',connector(tenant,user,'oidc','client-\u00e9','https://provider.oidc.test/realm')[1],['23514']),
  ('oidc-oversized-client',connector(tenant,user,'oidc','x'*256,'https://provider.oidc.test/realm')[1],['22001']),
  ('foreign-binding',binding(foreign,oidc,user,'foreign','oidc')[1],['23503']),
  ('profile-binding',binding(tenant,oidc,user,'123','gitea')[1],['23503']),
  ('empty-subject',binding(tenant,oidc,user,'','oidc')[1],['23514']),
  ('nonascii-subject',binding(tenant,oidc,user,'\u4e2d','oidc')[1],['23514']),
  ('oversized-subject',binding(tenant,oidc,user,'x'*256,'oidc')[1],['22001']),
  ('missing-nonce',flow(tenant,oidc,'oidc')[1],['23514']),
  ('short-nonce',flow(tenant,oidc,'oidc',blob(31))[1],['23514']),
  ('gitea-with-nonce',flow(tenant,old,'gitea',blob(32))[1],['23514']),
  ('profile-flow',flow(tenant,oidc,'gitea')[1],['23503']),
  ('foreign-flow',flow(foreign,oidc,'oidc',blob(32))[1],['23503'])]:sql(statement,name,codes)
 nonce,state=blob(32),blob(32);new_flow,statement=flow(tenant,oidc,'oidc',nonce,state);sql(statement,'oidc-pending-flow')
 sql(flow(tenant,oidc,'oidc',nonce)[1],'nonce-reuse',['23505']);sql(flow(tenant,oidc,'oidc',blob(32),state)[1],'state-reuse',['23505'])
 sql('UPDATE sso_connectors SET issuer=\'https://provider.oidc.test/other\',revision=revision+1,auth_version=auth_version+1 WHERE id='+literal(oidc)+';','issuer-immutable',['P0001'])
 sql('UPDATE sso_bindings SET provider=\'gitea\',revision=revision+1 WHERE id='+literal(bindings[0])+';','binding-profile-immutable',['P0001'])
 claim=uid();claim_update="UPDATE sso_flows SET state='claimed',claim_id="+literal(claim)+",verifier_key_id=NULL,verifier_nonce=NULL,verifier_ciphertext=NULL"
 sql(claim_update+',oidc_nonce_hash='+blob(32)+' WHERE id='+literal(new_flow)+';','nonce-immutable-during-claim',['P0001'])
 sql(claim_update+' WHERE id='+literal(new_flow)+';','claim-oidc-once')
 assert sql('SELECT oidc_nonce_hash='+nonce+" AND verifier_ciphertext IS NULL AND state='claimed' FROM sso_flows WHERE id="+literal(new_flow)+';','claimed-protection')=='t'
 sql("UPDATE sso_flows SET state='succeeded',authenticated_user_id="+literal(user)+',finished_at=clock_timestamp() WHERE id='+literal(new_flow)+';','complete-oidc')
 def session():return 'INSERT INTO sessions(id,user_id,sso_tenant_id,sso_connector_id,sso_binding_id,sso_auth_version,sso_flow_id) VALUES('+','.join([literal(uid()),literal(user),literal(tenant),literal(oidc),literal(bindings[0]),'1',literal(new_flow)])+');'
 sql(session(),'single-session-issuance');sql(session(),'session-replay',['P0001'])
 preserved=sql('SELECT to_jsonb(c)::text FROM sso_connectors c WHERE id='+literal(oidc)+';','oidc-before-refused-downgrade');sql((root/'profile.down.sql').read_text(),'refuse-lossy-downgrade',['P0001']);assert sql('SELECT to_jsonb(c)::text FROM sso_connectors c WHERE id='+literal(oidc)+';','oidc-after-refused-downgrade')==preserved;assert sql(snapshot,'legacy-after-refused-downgrade')==before
 (run/'success.json').write_text(json.dumps({'image':image,'sql_checks':len(records),'public_tables_before_after':tables,'scope':'isolated exact migration-17 SSO DDL with minimal prerequisite tables; not the complete installed schema or serving authority','network':'none','host_ports':[],'product_code_changed':False},indent=2)+'\n');print('OIDC_STORAGE_STRUCTURE_PASSED',run,flush=True)
finally:
 if container:
  execute(['docker','logs',container],'postgres-log',allow_failure=True)
  execute(['docker','stop','--timeout','15',container],'stop',allow_failure=True)
  execute(['docker','rm','--force','--volumes',container],'remove')
