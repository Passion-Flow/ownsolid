import base64,hashlib,json,re,subprocess,sys
from pathlib import Path
root=Path(__file__).parent
run=Path((root/'current-final-full').read_text().strip())
source=(run/'source').read_text().strip()
reports={app:json.loads((run/(app+'-report.json')).read_text()) for app in ['console','portal']}
wanted={'console-description-edit-en','console-description-edit-zh-CN','console-description-conflict-en','console-description-conflict-zh-CN','console-description-dark','portal-description-read-en','portal-description-read-zh-CN','portal-description-denied-en','portal-description-denied-zh-CN'}
images=Path('/Users/passion./.codex/visualizations/2026/11/08/01a1bc5a-6881-7790-88c0-62ef0b189235/gw120-description')/('qualified-'+source[:8])
images.mkdir(exist_ok=True)
found={}
def walk(suite):
 for spec in suite.get('specs',[]):
  for test in spec.get('tests',[]):
   for result in test.get('results',[]):
    assert result.get('retry',0)==0
    for attachment in result.get('attachments',[]):
     name=attachment.get('name','')
     if name in wanted and attachment.get('contentType')=='image/png':
      raw=base64.b64decode(attachment['body']);(images/(name+'.png')).write_bytes(raw)
      found[name]=hashlib.sha256(raw).hexdigest()
 for child in suite.get('suites',[]):walk(child)
for app,report in reports.items():
 assert report['stats']['unexpected']==0 and report['stats']['flaky']==0
 for suite in report['suites']:walk(suite)
assert set(found)==wanted,(set(found),wanted)
manifest={'source':source,'images':found,'console':reports['console']['stats'],'portal':reports['portal']['stats'],'image_directory':str(images)}
(run/'web-review.json').write_text(json.dumps(manifest,indent=2)+'\n')
if sys.argv[1]=='images':
 print(json.dumps(manifest,indent=2));raise SystemExit(0)
assert (run/'exit-status').read_text().strip()=='0'
assert (run/'stage').read_text().strip()=='complete'
c=reports['console']['stats'];p=reports['portal']['stats']
body=f"""

### Complete Web gate and final screen review

The exact integrated source {source} passed make check and make build.
The make test target passed with every Go module, Rust and Web unit target,
and both complete browser suites. A private Make overlay only capped
browser workers at four for Console and one for Portal, reused the same
production assets just built, and retained JSON reports. It selected no
subset and introduced no skip. Console passed {c['expected']} cases with
{c['skipped']} existing environment-dependent skips; Portal passed
{p['expected']} with {p['skipped']} existing skips. There were no unexpected
results, flaky cases or retries. These are workstation results, not a
new installed-server or GPU qualification.

The final focused checks passed all 52 shared UI unit tests and 29
Chromium cases: eight description flows, twelve Console state checks and
nine matching Portal checks. They include real backdrop clicks with an
unsaved draft in both languages, independent latest-alias reads, Undo
replay after a later writer, and a revision-refused Undo that directs the
person to review instead of offering a futile retry. The pinned old
Console build from d56269c3 failed the new historical-Undo assertion as
expected; its file manifest and failed report remain separate.

Nine final screenshots were read: English and Simplified Chinese editing,
reviewed current text, read-only catalogue detail and permission refusal,
plus the dark form. The three screen-review answers above still apply:
S-205 is a detail page with a short-form dialog whose primary action is
Save description; S-304 is a list with a preview drawer and the explicitly
unavailable Try a model action. Seven states remain defined, with distinct
initial and filtered empties. The narrow layouts fit, statuses use text,
and backdrop clicks preserve input while Escape restores the opener.
Screenshots and reports are retained in the source-keyed qualification
artifacts outside Git.

The first broad run at d56269c3 stopped in the installer tests after both
browser suites passed. Its temporary mount path was too long for the
Unix-socket fixtures. With umask 077, the published-model fixture was also
created as 0400 and faithfully backed up/restored that mode, while its
assertion expected 0444. A short physical temporary path and the fixture's
normal umask 022 passed the focused installer cases without source edits.
The final full run used that environment on an owned sparse APFS volume,
then detached and removed the volume and only its own build artifact.
The earlier failure is retained and is not reported as a successful gate.

Actual same-source installed Console-to-Portal description acceptance and
the Portal scope SQL proof remain open. POR-104 still also requires TEN-107
prices; this Web gate does not tick that item.
"""
note=Path('docs/research/gateway-model-description.md')
assert '### Complete Web gate and final screen review' not in note.read_text()
with note.open('a') as stream:stream.write(body)
roadmap=Path('docs/product/roadmap/portal.md');text=roadmap.read_text()
assert 'at bc3fa720' in text
roadmap.write_text(text.replace('at bc3fa720','at '+source[:8]))
print(json.dumps({'source':source,'console':c,'portal':p,'image_directory':str(images)},indent=2))
