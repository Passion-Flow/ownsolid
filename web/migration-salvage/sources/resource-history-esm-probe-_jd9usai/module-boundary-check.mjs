import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { dirname, join } from 'node:path';
import { readFileSync, writeFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { fileURLToPath } from 'node:url';
const root = dirname(fileURLToPath(import.meta.url));
const manifest = JSON.parse(readFileSync(join(root, 'correction-manifest.json'), 'utf8'));
const appRequire = createRequire(manifest.consolePackage);
const testRequire = createRequire(appRequire.resolve('@playwright/test/package.json'));
const playwrightPackage = testRequire.resolve('playwright/package.json');
const upstreamPath = join(dirname(playwrightPackage), 'lib/util.js');
const { fileIsModule } = testRequire(upstreamPath);
const actual = {
  oldExternalEntry: fileIsModule(manifest.oldEntry),
  correctedExternalEntry: fileIsModule(manifest.correctedEntry),
  mainConsoleEntry: fileIsModule(manifest.mainEntry),
};
assert.deepEqual(actual, { oldExternalEntry: false, correctedExternalEntry: true, mainConsoleEntry: true });
const result = {
  scope: 'Pinned upstream module-classification helper only; no tests loaded, no Playwright collection, browser or preview',
  version: JSON.parse(readFileSync(playwrightPackage, 'utf8')).version,
  selectorSource: upstreamPath,
  selectorSha256: createHash('sha256').update(readFileSync(upstreamPath)).digest('hex'),
  actual,
  passed: true,
  browserExecuted: false,
  discoveryExecuted: false,
  buildExecuted: false,
};
writeFileSync(join(root, 'module-boundary-result.json'), JSON.stringify(result, null, 2) + '\n');
console.log(JSON.stringify(result, null, 2));
