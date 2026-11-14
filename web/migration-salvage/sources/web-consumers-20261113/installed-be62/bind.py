from pathlib import Path
import json,os,subprocess,sys
root=Path(__file__).resolve().parent
if len(sys.argv)!=3:raise SystemExit('Use: bind.py WORKTREE FULL_FROZEN_SHA')
checkout=Path(sys.argv[1]).resolve();revision=sys.argv[2]
assert len(revision)==40 and all(c in '0123456789abcdef' for c in revision)
assert subprocess.check_output(['git','rev-parse','HEAD'],cwd=checkout,text=True).strip()==revision
assert subprocess.check_output(['git','status','--porcelain'],cwd=checkout,text=True).strip()==''
meta=root/'binding.json'
link=root/'node_modules'
if link.is_symlink():
 assert meta.exists() and os.readlink(link)==json.loads(meta.read_text())['dependencies']
 link.unlink()
else:assert not link.exists()
deps=checkout/'web/apps/console/node_modules';assert deps.is_dir();os.symlink(str(deps),link)
q=lambda p:json.dumps(str(p))
(root/'bound-source.ts').write_text('export const checkout = '+q(checkout)+';\nexport const revision = '+q(revision)+';\n')
(root/'runtime-source.ts').write_text('import english from '+q(checkout/'web/packages/i18n/locales/en.json')+' with { type: "json" };\nimport chinese from '+q(checkout/'web/packages/i18n/locales/zh-cn.json')+' with { type: "json" };\nexport { english, chinese };\nexport { expectFocusVisible } from '+q(checkout/'web/packages/shell/testing/keyboard-flows.ts')+';\n')
config = """import { defineConfig } from '@playwright/test';
import { lstatSync } from 'node:fs';
import { isAbsolute, join } from 'node:path';
import { liveOptions } from LIVE_IMPORT;
import { readFixture } from './fixture.ts';
if (process.env.OWNSOLID_GW120_FIXTURE_FILE) readFixture(process.env.OWNSOLID_GW120_FIXTURE_FILE);
const fixture = process.env.OWNSOLID_GW120_FIXTURE_FILE;
const output = process.env.OWNSOLID_GW120_OUTPUT_DIR;
if (fixture && (!output || !isAbsolute(output))) throw new Error('Use run.py to create a fresh private evidence directory');
if (output) {
  const info = lstatSync(output);
  if (!info.isDirectory() || info.uid !== process.getuid?.() || (info.mode & 0o077) !== 0) throw new Error('Evidence directory must be owned, regular and private');
}
const destination = output ?? DISCOVERY_DIR;
export default defineConfig({ testDir: TEST_DIR, testMatch: 'description.spec.ts', outputDir: join(destination, 'artifacts'), workers: 1, retries: 0, reporter: [['line'], ['json', { outputFile: join(destination, 'report.json') }]], use: { ...liveOptions, trace: 'off', video: 'off', screenshot: 'off' } });
"""
config=config.replace('LIVE_IMPORT',q(checkout/'web/packages/shell/testing/live.ts')).replace('DISCOVERY_DIR',q(root/'discovery-results')).replace('TEST_DIR',q(root))
(root/'playwright.config.ts').write_text(config)
(root/'help.config.ts').write_text(config.replace("testMatch: 'description.spec.ts'","testMatch: 'parameters.spec.ts'"))
scope_config=config.replace("testMatch: 'description.spec.ts'","testMatch: 'live-workspace-scope.spec.ts'").replace(q(root),q(checkout/'web/apps/portal/e2e'),1)
(root/'scope.config.ts').write_text(scope_config)
(root/'tsconfig.json').write_text(json.dumps({'extends':str(checkout/'web/tsconfig.base.json'),'compilerOptions':{'types':['node'],'typeRoots':[str(deps/'@types')]},'include':['*.ts']},indent=2)+'\n')
with (root/'runtime-source.ts').open('a') as extra:
 extra.write('export { adminPassword, email, origin } from '+q(checkout/'web/packages/shell/testing/live.ts')+';\nexport { expectContractAudience } from '+q(checkout/'web/packages/shell/testing/contract-download.ts')+';\n')
meta.write_text(json.dumps({'source':revision,'worktree':str(checkout),'dependencies':str(deps),'state':'candidate binding; rebind to the new clean frozen checkout before an installed run'},indent=2)+'\n')
print(revision)
