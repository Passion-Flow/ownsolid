from pathlib import Path
import hashlib,json,subprocess
root=Path(__file__).resolve().parent
worktree=Path("/Users/passion./Documents/Passion's WorkSpace/Development/lane-web")
def sha(path):
 return hashlib.sha256(path.read_bytes()).hexdigest()
def git(*args):
 return subprocess.check_output(['git',*args],cwd=worktree,text=True).strip()
qualified=json.loads((root/'final-qualification.json').read_text())
published=(root/'published-source').read_text().strip()
assert git('rev-parse','HEAD')==published
assert not git('status','--porcelain')
run=Path((root/'publication-current-path').read_text().strip())
assert (run/'browser-input-diff').read_text()==''
assert (run/'stage').read_text().strip()=='complete'
assert (run/'exit-status').read_text().strip()=='0'
for name,digest in qualified['imagesRead'].items():
 assert sha(Path(name))==digest, name
immutable={
 'qualified-be62-web-build.tar.gz':'a4bd543164852f5c381227da5bdb2af4d40777079d080856b087c743e0a44434',
 'installed-entry-be62-inputs.tar.gz':'c2cd577bb55a89ae57f4c542c26f8ffc187b0948f70406ac661ba3a7b0747ba4',
 'installed-entry.md':'082c76f583fc3d58d2cd4e0f58d6c5a798ff528deb8df930891169723676dd48',
 'installed-entry-manifest.json':'652456d2046922817e40bb58e7a367fefac22c222a230e58732630117ef6dbef',
}
for name,digest in immutable.items(): assert sha(root/name)==digest,name
manifest={
 'publishedSource':published,'qualifiedSource':qualified['source'],
 'completeGate':qualified['run'],'completeGateStats':qualified['stats'],
 'publicationRun':str(run),'publicationBrowserInputDiff':[],
 'publicationRepeated':'make check; make build; make -o test-web-browser test',
 'publicationBrowserReuse':'Exact Web, API, fixtures, generators, capability declaration and Makefile bytes compared with qualified source',
 'originalAuthorSeries':(run/'original-author-series').read_text().splitlines(),
 'seriesPatch':{'path':str(run/'original-author-series.patch'),'sha256':sha(run/'original-author-series.patch')},
 'imagesRead':qualified['imagesRead'],'immutableInstalledInputs':immutable,
 'actualInstalledRun':False,'independentSQLRun':False,
 'roadmapTicksAdded':False,
}
(root/'consumer-handoff-manifest.json').write_text(json.dumps(manifest,indent=2)+'\n')
text=f'''# Web consumer handoff\n\nPublished source: `{published}`. Full gate source: `{qualified['source']}`.\n\n## Delivered consumers\n\n- AUD-108: localize the audit family and `audit.events_read` in the existing list/preview. Keep category filtering, raw action/request identity and the thirteen-field public shape. No exact-action enum is added.\n- MOD-121: distinguish expired and pending-cleanup uploads; stop terminal transfers; explain disabled actions; retain known state on transient refresh but remove it after denied reads. A fresh form creates a distinct intent key. Help search finds the visible lifecycle term in both languages.\n- DEL-109 documentation: use the existing installer `control_settings.runtime.licensing.maximum_imports` input; generated Control edits are overwritten. Materialization checks do not establish installed quota acceptance.\n\n## Verification\n\nThe complete `make check`, `make build`, `make test` gate passed at the exact full-gate source: Console 1,134 passed/28 existing conditional skips; Portal 410 passed/10 existing conditional skips; no unexpected/flaky/retried cases. Raw artifacts: `{qualified['run']}`.\n\nAfter rebase, browser inputs were byte-identical. Publication repeated every other check/build/test target and reused that exact complete browser result. Publication artifacts: `{run}`. Original-author series and its patch are recorded in the adjacent manifest.\n\nThe old production build fails the corrected English expired-search assertion and retains the Chinese positive control. The earlier unfiltered-article false positive and interrupted listener-loss run are retained as failures, not gate evidence. Ten final screenshots were opened and read; their exact paths and hashes are in the adjacent manifest.\n\n## Required screen answers\n\n| Screen | Archetype and fixed structure | Seven states and empty copy | One primary task |\n| --- | --- | --- | --- |\n| Audit | List plus preview: title, filters, table; event details in the existing drawer | Existing loading, first empty, filtered empty, partial, error, denied and unavailable states; no records differs from no events match | Locate and read an event |\n| Import status | Existing three-step flow ending in a status detail; no added modal | Explicit loading, initial history empty, filtered library empty, retained partial read, retryable error, denied and unavailable decisions; history empty differs from filtered library empty | Expired: Upload a new bundle; pending cleanup explains why writes are unavailable |\n| Help | Reference detail with searchable index and article sections | Loading/error/denied/unavailable behavior retained; built-in first-time empty is inapplicable and filtered no-article match has its own recovery | Find the lifecycle answer |\n\n## Installation boundary\n\nThe be62 Web/OpenAPI tree, asset archive, client bundle and entry manifests remain unchanged. This consumer publication does not qualify a new installed instance. Formal new-instance inputs and source binding must come from Delivery. Run GW120, Admin/Portal Help, Scope, independent native SQL, CLI, then original OBS7 in that order. Do not reuse old4463, rebind old evidence in place, or substitute these development assets into the frozen be62 instance.\n\nActual AUD-108 append/collector accounting, installed MOD-121 expiry and startup binding, Scope SQL and other owner acceptance remain open. No roadmap box was ticked from preview evidence.\n'''
(root/'consumer-handoff.md').write_text(text)
print(json.dumps({'handoff':str(root/'consumer-handoff.md'),'manifest':str(root/'consumer-handoff-manifest.json'),'published':published}))
