from pathlib import Path
import hashlib,json,subprocess
root=Path(__file__).resolve().parent
repo=Path("/Users/passion./Documents/Passion's WorkSpace/Development/lane-web")
run=Path((root/'heading-run-path').read_text().strip())
head=subprocess.check_output(['git','rev-parse','HEAD'],cwd=repo,text=True).strip()
assert head=='7401ff675b3259e018e3ba148ed63ae2331b11bd'
assert not subprocess.check_output(['git','status','--porcelain'],cwd=repo,text=True).strip()
for key,value in [('help.search','Search help'),('models.title','Model library')]:
 assert json.loads((repo/'web/packages/i18n/locales/en.json').read_text())[key]==value
files={str(p.relative_to(repo)):hashlib.sha256(p.read_bytes()).hexdigest() for p in (repo/'web/apps/console/dist').rglob('*') if p.is_file()}
(run/'old-console-assets.json').write_text(json.dumps({'source':head,'files':files},indent=2)+'\n')
f=repo/'docs/research/model-import-cleanup.md'
with f.open('a') as stream:stream.write('''
The Help index searches titles, summaries and section headings, without stemming.
Use Expired uploads as the English heading so the visible Expired lifecycle term
finds this answer. The bilingual consumer must find the article from Help search
before reading its new-key guidance. Preserve a controlled old production build:
the old expires heading does not match expired, while the Chinese heading already
matches the equivalent query. This changes article wording only, not search scope.
''')
f=repo/'web/packages/help/locales/en/console/model-library.md'
t=f.read_text();assert '## If an upload expires\n' in t;f.write_text(t.replace('## If an upload expires\n','## Expired uploads\n'))
f=repo/'web/apps/console/e2e/models.spec.ts'
t=f.read_text();old="    await expect(old).toBeVisible();\n    await page.goto('/help/model-library');"
new='''    await expect(old).toBeVisible();
    await page.goto('/help');
    await page.getByLabel(messages['help.search'], { exact: true }).fill(locale === 'en' ? 'expired' : __EXPIRY_QUERY__);
    const answer = page.getByRole('main').getByRole('link', { name: new RegExp(`^${messages['models.title']}`) });
    await expect(answer).toBeVisible();
    await answer.click();'''
new=new.replace('__EXPIRY_QUERY__',json.dumps(chr(0x8fc7)+chr(0x671f)))
assert old in t;f.write_text(t.replace(old,new))
