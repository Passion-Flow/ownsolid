from pathlib import Path
import datetime,hashlib,json,re,sys
root=Path(sys.argv[1]).resolve();text=(root/'installed.log').read_text();manifest=json.loads((root/'evidence-manifest.json').read_text())
assert hashlib.sha256((root/'installed.log').read_bytes()).hexdigest()==manifest['files']['installed.log']['sha256']
match=re.search(r'fleet_load_test.go:\d+: report:\n',text);assert match
report,_=json.JSONDecoder().raw_decode(text[match.end():].lstrip());assert report['agents']==2000
steady=[w for w in report['windows'] if 60<w['end_seconds']<=660];assert [w['end_seconds'] for w in steady]==list(range(120,661,60))
late=[w for w in report['windows'] if w['end_seconds']>=1170];assert [w['end_seconds'] for w in late]==list(range(1200,1441,60))
failures=[]
for w in steady:
 if w['connected']!=2000 or w['calls']['heartbeat']['failed'] or w['calls']['claim']['failed']:failures.append({'steady':w})
 if w['calls']['heartbeat']['p99_milliseconds']>1000:failures.append({'heartbeat_p99':w})
for w in late:
 if w['on_stream']!=2000:failures.append({'late_stream':w['end_seconds'],'count':w['on_stream']})
summary=json.loads(re.search(r'fleet_load_test.go:\d+: summary: (\{[^\n]+\})',text).group(1))
if summary['dispatch_p99_seconds']>5 or summary['restart_reconnect_seconds']>300:failures.append({'summary_threshold':summary})
for fault in report['faults']:
 if fault['measured'] and (fault['full_recovery_seconds'] is None or fault['full_recovery_seconds']>300 or fault['recovered_agents']!=fault['agents']):failures.append({'fault':fault})
tasks=report['tasks'];assert tasks=={'leases':1100,'acknowledged':1100,'succeeded':1100,'resyncs':0,'lost_replies':0}
if report['connected_at_end']!=2000 or report['on_stream_at_end']!=2000 or report['revoked']:failures.append({'terminal':{k:report[k] for k in ('connected_at_end','on_stream_at_end','revoked')}})
long=re.search(r'long tasks: (\d+) on their first attempt, ([\d.]+) s',text);assert long and int(long.group(1))==100
rates={}
for m in re.finditer(r'(steady stretch|whole run), per second over (\d+) s: (\d+) commits, (\d+) rows inserted, (\d+) updated, (\d+) deleted, (\d+) KB of WAL',text):
 label,seconds,commits,inserted,updated,deleted,wal=m.groups();rates[label]={'window_seconds':int(seconds),'commits_per_second_rounded':int(commits),'inserted_rows_per_second_rounded':int(inserted),'updated_rows_per_second_rounded':int(updated),'deleted_rows_per_second_rounded':int(deleted),'sum_of_rounded_row_modifications_per_second':int(inserted)+int(updated)+int(deleted),'wal_kib_per_second_rounded':int(wal)}
assert rates['steady stretch']['window_seconds']==600
observations=[json.loads(l) for l in (root/'raw/database-observations.jsonl').read_text().splitlines()];assert observations
state=observations[0]['state'];assert all(o['state']==state for o in observations) and state['stats_reset'] is None and state['track_counts']=='on'
stamp=re.sub(r'(\.\d{6})\d+(?=Z|[+-])',r'\1',report['started_at']).replace('Z','+00:00');start=datetime.datetime.fromisoformat(stamp).timestamp();assert observations[0]['at']<start+60 and observations[-1]['at']>start+660
database=(root/'raw/database-container-state.log').read_text().strip();decoder=json.JSONDecoder();db,offset=decoder.raw_decode(database);assert database[offset:].strip()=='0' and not db['OOMKilled']
probe=re.search(r'zero egress: no outbound attempt recorded \((\d+) harness probes seen\)',text);assert probe and int(probe.group(1))>=1
passed='--- PASS: TestFleetLoad' in text and all(v=='0' for v in manifest['exits'].values()) and not failures and '--- SKIP:' not in text and '--- FAIL:' not in text
result={'source':manifest['source_after_run'],'passed':passed,'failures':failures,'summary':summary,'steady_heartbeat_p99_bucket_upper_ms':max(w['calls']['heartbeat']['p99_milliseconds'] for w in steady),'steady_windows':steady,'required_stream_windows':[{'end_seconds':w['end_seconds'],'on_stream':w['on_stream']} for w in late],'rates':rates,'faults':report['faults'],'tasks':tasks,'long_tasks_first_attempt':int(long.group(1)),'long_tasks_total_span_seconds':float(long.group(2)),'database_state_observations':len(observations),'database_statistics_invariant':state,'database_restart_count':0,'database_oom_killed':False,'product_egress_attempts':0,'recorder_probes':int(probe.group(1)),'selected_log_sha256':manifest['files']['installed.log']['sha256']}
out=root/'analyzed-kpis.json';assert not out.exists();out.write_text(json.dumps(result,indent=2)+'\n')
print(json.dumps({k:v for k,v in result.items() if k!='steady_windows'},indent=2))
