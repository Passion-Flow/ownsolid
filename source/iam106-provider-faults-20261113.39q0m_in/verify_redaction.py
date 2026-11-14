import base64,hashlib,html,json,pathlib,re,sys,urllib.parse
def has_credential(raw,values):
    if any(value in raw for value in values) or re.search(r'-----BEGIN (?:RSA |EC |ENCRYPTED )?PRIVATE KEY-----',raw):return True
    for match in re.finditer(r'(?<![A-Za-z0-9_-])([A-Za-z0-9_-]{12,})\.[A-Za-z0-9_-]+\.[A-Za-z0-9_.-]+',raw):
        try:header=json.loads(base64.urlsafe_b64decode(match.group(1)+'='*(-len(match.group(1))%4)))
        except (ValueError,UnicodeError):continue
        if isinstance(header,dict) and 'alg' in header:return True
    return False

def inspect(root):
    p=pathlib.Path(root);values=set();processes=[]
    for file in list(p.glob('*/artifacts/*/sensitive-inputs.json'))+list(p.glob('*/database-sensitive-inputs.json')):
        for value in json.loads(file.read_text()):
            if len(value)>=16:
                values.update((value,html.escape(value),json.dumps(value)[1:-1],urllib.parse.quote(value,safe=''),urllib.parse.quote_plus(value,safe='')))
    for file in p.glob('*/artifacts/*/processes.json'):
        record=json.loads(file.read_text());assert record['processes'];processes.extend(record['processes'])
        for process in record['processes']:
            log=file.parent/process['log'];assert log.is_file() and '--- PASS: TestOPChildServer' in log.read_text()
    assert values and processes and all(x['exited'] and x['success'] for x in processes)
    files=list(p.rglob('*.log'));assert len(files)>10
    for file in files:
        raw=file.read_text(errors='replace')
        if has_credential(raw,values):raise RuntimeError('sensitive fixture credential detected in retained log '+str(file.relative_to(p)))
    result={'log_files':len(files),'private_vocabulary_variants':len(values),'child_processes':len(processes),'all_children_exited_successfully':True,'sensitive_values_found':0,'private_values_published':False,'log_hashes':{str(f.relative_to(p)):hashlib.sha256(f.read_bytes()).hexdigest() for f in files}}
    (p/'redaction-verification.json').write_text(json.dumps(result,indent=2)+'\n');print('PRIVATE_LOG_REDACTION_VERIFIED',len(files),len(processes),flush=True)

if __name__=="__main__":inspect(sys.argv[1])
