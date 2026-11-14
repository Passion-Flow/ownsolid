import json,pathlib,subprocess,shutil
p=pathlib.Path(__file__).parent
subprocess.run(['python3',str(p/'qualify-protocol.py')],check=True)
s=pathlib.Path((p/'active-qualification').read_text().strip())
assert (s/'success.json').is_file()
graph=s/'artifacts/group-graph.json';assert graph.is_file()
shutil.copy2(graph,p/'actual-group-graph.json')
(p/'protocol-result.json').write_text(json.dumps({'stage':str(s),'graph_file':str(graph)},indent=2)+'\n')
subprocess.run(['python3',str(p/'qualify-storage.py')],check=True)
