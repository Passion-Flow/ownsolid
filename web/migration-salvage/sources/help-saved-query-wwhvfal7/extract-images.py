from pathlib import Path
import base64,hashlib,json,re,sys
root=Path(sys.argv[1]);info=json.loads((root/'preparation.json').read_text())
dest=Path('/Users/passion./.codex/visualizations/2026/11/08/01a1bc5a-6881-7790-88c0-62ef0b189235/help-api-restoration')/('qualified-'+info['focusedSource'][:8])
assert not dest.exists();dest.mkdir(parents=True)
def cases(suites):
 for suite in suites:
  for spec in suite.get('specs',[]):
   for test in spec.get('tests',[]):yield spec,test
  yield from cases(suite.get('suites',[]))
images={}
for app in ['console','portal']:
 report=json.loads((root/('focused-'+app+'.json')).read_text())
 assert report['stats']['expected']==2 and report['stats']['unexpected']==0
 for spec,test in cases(report['suites']):
  assert test['results'][0]['status']=='passed'
  for item in test['results'][0].get('attachments',[]):
   if item.get('contentType')!='image/png':continue
   name=app+'-'+re.sub('[^A-Za-z0-9._-]','-',item['name'])+'.png'
   path=dest/name;assert not path.exists()
   body=base64.b64decode(item['body']) if item.get('body') else Path(item['path']).read_bytes()
   path.write_bytes(body);images[str(path)]=hashlib.sha256(body).hexdigest()
assert len(images)==12,len(images)
(root/'screenshots-to-read.json').write_text(json.dumps(images,indent=2)+'\n')
print(json.dumps({'images':list(images),'count':len(images)},indent=2))
