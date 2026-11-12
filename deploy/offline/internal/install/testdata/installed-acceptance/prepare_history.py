"""Acceptance-only synthetic samples on the installation's existing local cell."""
import argparse
import datetime as dt
import hashlib
import json
import os
from pathlib import Path
import ssl
import stat
import subprocess
import time
import urllib.parse
import urllib.request
import uuid

def command(*args, timeout=30):
    return subprocess.run(args, check=True, timeout=timeout, capture_output=True, text=True).stdout


def utc(value):
    return dt.datetime.fromtimestamp(value, dt.timezone.utc).strftime('%Y-%m-%dT%H:%M:%SZ')


def load(path):
    info = path.lstat()
    if not path.is_file() or path.is_symlink() or info.st_uid != os.geteuid() or info.st_mode & 0o077:
        raise ValueError('Use an owned protected regular input')
    return json.loads(path.read_text())


def save(path, value):
    temp = path.with_name(path.name+'.stage')
    fd = os.open(temp, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, 'w') as stream:
        json.dump(value, stream, indent=2)
        stream.write('\n')
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temp, path)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def client_input(ready, scope, policy, windows, source_sha, run_id):
    """Bind the new Linux driver to this installation without reading credentials."""
    return {
        'source_sha': source_sha,
        'admin': {'origin': ready['admin_origin'], 'ca_file': '/evidence/installation-ca.pem',
                  'dial_address': '127.0.0.1:8443'},
        'raw': {'origin': policy['raw_origin'], 'ca_file': '/evidence/history-inputs/raw-ca.pem'},
        'long': {'origin': policy['long_origin'], 'ca_file': '/evidence/history-inputs/long-ca.pem'},
        'email': ready['administrator_email'], 'password_file': '/evidence/admin-password',
        'workspace_id': scope['primary_workspace'], 'foreign_workspace_id': scope['foreign_workspace'],
        'site_id': windows['raw']['cells'][0]['site_id'], 'cell_id': windows['raw']['cells'][0]['cell_id'],
        'session_file': '/evidence/history-session-' + run_id + '.json', 'timeout_seconds': 20,
        'raw_window': windows['raw'], 'rollup_window': windows['rollup'],
        'required_commands': ['read', 'long-unavailable', 'long-restored', 'long-tls-refused',
                              'long-tls-restored', 'workspace-disabled', 'workspace-restored', 'logout-replay'],
    }


def docker_command(socket):
    """Use the parent's explicit local daemon, ignoring ambient Docker settings."""
    if not Path(socket).is_absolute() or not stat.S_ISSOCK(Path(socket).stat().st_mode):
        raise ValueError('Select the admitted local Unix Docker socket')
    return ('docker', '--host', 'unix://' + socket)


def main(root, bundle, container, run_id, docker_socket):
    if root.resolve() != root or not root.is_dir():
        raise ValueError('Use the canonical evidence directory')
    source_sha = (bundle/'source-sha').read_text().strip()
    if str(uuid.UUID(run_id)) != run_id:
        raise ValueError('Use the admitted run UUID')
    if not container.startswith('lane-del-'):
        raise ValueError('Operate only on the admitted DEL namespace')
    docker = docker_command(docker_socket)
    control = load(root/'control.json')
    if control['run_id'] != run_id or control['phase'] != 'history-preparation' or control['driver'] is not None:
        raise ValueError('History preparation has no pending handoff')
    label = command(*docker,'inspect','--format','{{index .Config.Labels "io.ownsolid.acceptance.run_id"}}',container).strip()
    if label != run_id:
        raise ValueError('The namespace belongs to a different run')
    route = command('ip', 'route', 'show', 'default').strip().splitlines()
    if len(route) != 1 or ' dev egress0' not in ' '+route[0] or ' via ' in ' '+route[0]:
        raise ValueError('Run in the existing outer network namespace with only the recorder default')
    evidence = root
    provenance = load(evidence/'installed-provenance.json')
    if provenance['build_revision'] != source_sha or provenance['build_modified'] != 'false':
        raise ValueError('Installed Control is not the frozen clean build')
    scope = load(evidence/'scope-state.json')
    tenant, workspace = scope['primary_tenant'], scope['primary_workspace']
    if str(uuid.UUID(tenant)) != tenant or str(uuid.UUID(workspace)) != workspace:
        raise ValueError('Invalid installed scope UUID')
    catalog = load(evidence/'schema-table-set-proof.json')
    if catalog['source'] != source_sha or not catalog['exact_migration_ledger_verified']:
        raise ValueError('The installed schema proof differs from this source')
    site, cell_id = catalog['local_cell']['site_id'], catalog['local_cell']['cell_id']
    policy = load(evidence/'history-inputs/monitoring.json')['fleet_history']
    clients = {}
    for tier in ('raw','long'):
        context = ssl.create_default_context(cafile=str(evidence/f'history-inputs/{tier}-ca.pem'))
        context.minimum_version = ssl.TLSVersion.TLSv1_2
        clients[tier] = urllib.request.build_opener(urllib.request.ProxyHandler({}),urllib.request.HTTPSHandler(context=context),NoRedirect())

    def request(tier, path, data=None):
        req = urllib.request.Request(policy[tier+'_origin']+path, data=data,
                                     headers={'Content-Type':'text/plain'} if data is not None else {})
        with clients[tier].open(req, timeout=15) as response:
            return response.read(2 << 20)

    seed_file = evidence/'history-seed.json'
    if seed_file.exists():
        seed = load(seed_file)
        if seed['source_sha'] != source_sha or seed['workspace_id'] != workspace or seed['run_id'] != run_id:
            raise ValueError('Existing seed belongs to another source or scope')
    else:
        now = int(time.time())//60*60
        seed = {'source_sha':source_sha,'run_id':run_id,'tenant_id':tenant,'workspace_id':workspace,
                'site_id':site,'cell_id':cell_id,'nodes':[str(uuid.uuid4()),str(uuid.uuid4())],
                'raw_from':now-600,'raw_to':now-120,'rollup_from':now-7200,'rollup_to':now-6720,
                'status':'prepared','scope':'synthetic up samples; one existing installed cell; no physical-node or five-cell claim'}
        save(seed_file,seed)
    now = int(time.time())//60*60
    if seed['raw_from']-45 < now-policy['raw_horizon_seconds']:
        archive = evidence/('history-seed-before-raw-'+str(seed['raw_from'])+'.json')
        if not archive.exists():
            save(archive,seed)
        seed.update(raw_from=now-600,raw_to=now-120,status='prepared')
        save(seed_file,seed)
    lines=[]
    for tier in ('raw','rollup'):
        start,end = seed[tier+'_from'],seed[tier+'_to']
        for at in range(start-60,end+1,15):
            if start+180 <= at < start+420:
                continue
            for node in seed['nodes']:
                labels={'job':'ownsolid-node','tenant_id':tenant,'workspace_id':workspace,
                        'site_id':site,'cell_id':cell_id,'node_id':node}
                encoded=','.join(k+'='+json.dumps(v) for k,v in labels.items())
                lines.append('up{'+encoded+'} 0 '+str(at*1000))
    payload=('\n'.join(lines)+'\n').encode()
    request('raw','/api/v1/import/prometheus',payload)
    request('raw','/internal/resetRollupResultCache')
    labels=','.join(k+'='+json.dumps(v) for k,v in {'tenant_id':tenant,'workspace_id':workspace,'site_id':site,'cell_id':cell_id}.items())
    grouping=' by (tenant_id, workspace_id, site_id, cell_id) '
    raw_source='last_over_time(up{job="ownsolid-node",'+labels+'}[45s])'
    def verify(tier, start, end, step, observed, reachable):
        expr='label_set(label_del('+observed+',"__name__"),"measure","observed") or label_set(label_del('+reachable+',"__name__"),"measure","reachable")'
        query=urllib.parse.urlencode({'query':expr,'start':start,'end':end,'step':step,'nocache':1,'latency_offset':'1ms','deny_partial_response':1})
        deadline=time.monotonic()+20
        while True:
            result=json.loads(request(tier,'/api/v1/query_range?'+query))
            if result.get('status')!='success' or result.get('isPartial') or result.get('warnings'):
                raise ValueError('The native store returned incomplete preparation data')
            series=result.get('data',{}).get('result',[])
            measures={row['metric'].get('measure'):row for row in series}
            if len(series)==2 and set(measures)=={'observed','reachable'}:
                times=[]
                for measure,expected in [('observed',2),('reachable',0)]:
                    row=measures[measure]
                    if any(row['metric'].get(k)!=v for k,v in {'tenant_id':tenant,'workspace_id':workspace,'site_id':site,'cell_id':cell_id}.items()):
                        raise ValueError('The native preparation returned foreign scope')
                    values=row.get('values',[])
                    if not values or any(float(value)!=expected for _,value in values):
                        raise ValueError('The native preparation counts differ')
                    times.append([at for at,_ in values])
                if times[0]!=times[1] or not 0<len(times[0])<(end-start)//step+1:
                    raise ValueError('Native preparation did not retain sparse measured zero')
                return len(times[0])
            if time.monotonic()>=deadline:
                raise ValueError('Native preparation samples did not become visible')
            time.sleep(.2)
    raw_points=verify('raw',seed['raw_from'],seed['raw_to'],15,'count'+grouping+'('+raw_source+')','sum'+grouping+'('+raw_source+' == bool 1)')
    verify('raw',seed['rollup_from'],seed['rollup_to'],60,'count'+grouping+'('+raw_source+')','sum'+grouping+'('+raw_source+' == bool 1)')
    actual = json.loads(command(*docker,'exec',container,'docker','compose','-f','/srv/fleet-history/compose.yaml','config','--format','json'))
    evaluator = actual['services']['evaluator']['command']
    replay = [*docker,'exec',container,'docker','compose','-f','/srv/fleet-history/compose.yaml',
              'run','--rm','--no-deps','--no-TTY','--pull','never','--name','lane-del-replay-'+run_id[:8],'evaluator',
              *evaluator,'-replay.timeFrom='+utc(seed['rollup_from']),'-replay.timeTo='+utc(seed['rollup_to']),'-datasource.queryStep=1m']
    try:
        result=subprocess.run(replay,check=True,timeout=120,capture_output=True,text=True)
        (evidence/'history-replay.log').write_text(result.stdout+result.stderr)
        (evidence/'history-replay.log').chmod(0o600)
    except BaseException:
        subprocess.run([*docker,'exec',container,'docker','rm','--force','lane-del-replay-'+run_id[:8]],capture_output=True,timeout=30)
        raise
    for tier in ('raw','long'):
        request(tier,'/internal/resetRollupResultCache')
    # All native probes finish before the driver can claim its counter baseline.
    observed='last_over_time(ownsolid:workspace_cell_nodes_observed:count{'+labels+'}[1m])'
    reachable='last_over_time(ownsolid:workspace_cell_nodes_reachable:count{'+labels+'}[1m])'
    replay_points=verify('long',seed['rollup_from'],seed['rollup_to'],60,observed,reachable)
    seed.update(status='native_replay_verified',samples=len(lines),raw_input_sha256=hashlib.sha256(payload).hexdigest(),
                evaluator_command=evaluator,replay_observed_points=replay_points,raw_observed_points=raw_points)
    save(seed_file,seed)
    windows={tier:{'workspace_id':workspace,'from':utc(seed[tier+'_from']),'to':utc(seed[tier+'_to']),
                   'cells':[{'site_id':site,'cell_id':cell_id,'observed':2,'reachable':0}],
                   'require_sparse':True,'require_zero':True} for tier in ('raw','rollup')}
    save(evidence/'history-windows.json',windows)
    ready=load(evidence/'acceptance-ready.json')
    session='/evidence/history-session-'+run_id+'.json'
    if (evidence/Path(session).name).exists():
        raise ValueError('A session already exists for this window; do not reinitialize')
    config=client_input(ready,scope,policy,windows,source_sha,run_id)
    save(evidence/'obs-history-input.json',config)
    ready.update(driver_input_sha256=hashlib.sha256((evidence/'obs-history-input.json').read_bytes()).hexdigest(),
                 history_windows=windows,counter_quiet_at=utc(int(time.time())))
    save(evidence/'acceptance-ready.json',ready)
    print(json.dumps({'event':'INSTALLED_NATIVE_REPLAY_SEED_VERIFIED','source_sha':source_sha,
                      'workspace_id':workspace,'site_id':site,'cell_id':cell_id,'samples':len(lines),
                      'raw_points':raw_points,'rollup_points':replay_points,'input_sha256':ready['driver_input_sha256']}))

if __name__=='__main__':
    parser=argparse.ArgumentParser()
    parser.add_argument('--evidence',type=Path,required=True)
    parser.add_argument('--bundle',type=Path,required=True)
    parser.add_argument('--container',required=True)
    parser.add_argument('--run-id',required=True)
    parser.add_argument('--docker-socket',required=True)
    args=parser.parse_args()
    main(args.evidence,args.bundle,args.container,args.run_id,args.docker_socket)
