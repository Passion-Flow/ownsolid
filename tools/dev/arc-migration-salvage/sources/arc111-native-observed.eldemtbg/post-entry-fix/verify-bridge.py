from pathlib import Path
import ast,hashlib,json,os,select,time,types

root=Path(__file__).resolve().parent
source=(root/'remote-guard.corrected-source-bytes').read_text()
module=ast.parse(source)
functions=ast.Module(body=[n for n in module.body if isinstance(n,ast.FunctionDef) and n.name in ['receive_payload','read_admission_payload']],type_ignores=[])
assert len(functions.body)==2
results=[]
for close_writer in [True,False]:
    rfd,wfd=os.pipe()
    stream=os.fdopen(rfd,'rb',buffering=0)
    data=(root.parent/'payload.tar').read_bytes()[:512]
    os.write(wfd,data)
    if close_writer:
        os.close(wfd)
        wfd=None
    record={}
    deadline=time.monotonic()+(.5 if close_writer else .03)
    saves=[]
    namespace={'os':os,'select':select,'time':time,'hashlib':hashlib,
               'sys':types.SimpleNamespace(stdin=types.SimpleNamespace(buffer=stream)),
               'record':record,'phase_end':deadline,'work_end':deadline+1,
               'save':lambda:saves.append(True)}
    exec(compile(functions,'actual-os-pipe-bridge','exec'),namespace)
    error=None
    try:
        got=namespace['read_admission_payload'](len(data),hashlib.sha256(data).hexdigest())
        assert close_writer and got==data
    except TimeoutError as caught:
        error=str(caught)
        assert not close_writer and error=='PAYLOAD_ADMISSION_DEADLINE_EXCEEDED'
    finally:
        assert os.get_blocking(rfd)
        stream.close()
        if wfd is not None:
            os.close(wfd)
    assert len(saves)==1
    receipt=record['payload_receive']
    assert receipt['received_bytes']==512 and receipt['eof_observed']==close_writer
    results.append({'case':'real_pipe_eof' if close_writer else 'real_pipe_missing_eof_deadline',
                    'error':error,'receive_receipt':receipt,'own_pipe_fds_closed':True,
                    'scope':'controlled local OS pipe, not the actual failed transfer'})
result={'state':'PASS','os_pipe_cases':results,'network_connections':0,'child_processes':0,
        'compilers_services_or_databases_started':0,'candidate_activated':False,
        'corrected_remote_sha256':hashlib.sha256(source.encode()).hexdigest()}
(root/'bridge-result.json').write_text(json.dumps(result,indent=2,sort_keys=True)+'\n')
print(json.dumps({'state':'PASS','real_os_pipe_cases':2,'network_connections':0,'own_pipe_fds_closed':True},indent=2))
