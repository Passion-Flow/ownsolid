// Private, configuration-only proof; never part of the production lint implementation.
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { dirname, resolve } from 'node:path';
import { pathToFileURL } from 'node:url';

const cwd = resolve(process.argv[2]);
const require = createRequire(pathToFileURL(resolve(cwd, 'package.json')));
const root = dirname(require.resolve('eslint/package.json'));
const { ESLint } = require('eslint');
const helpers = require(resolve(root, 'lib/eslint/eslint-helpers.js'));
const { WarningService } = require(resolve(root, 'lib/services/warning-service.js'));
const { lintPartitions } = await import(pathToFileURL(resolve(cwd, 'packages/config/lint.js')));
const configFile = 'packages/config/eslint.config.js';
const options = helpers.processOptions({ cwd, overrideConfigFile: configFile });
const warning = new WarningService();
const linter = helpers.createLinter(options, warning);
const configLoader = helpers.createConfigLoader(options, helpers.createDefaultConfigs(), linter, warning);
const files = (patterns, errorOnUnmatchedPattern) => helpers.findFiles({
  patterns, cwd, globInputPaths: true, errorOnUnmatchedPattern, configLoader,
});
const original = (await files(['.'], true)).sort();
const partitions = [];
const selected = new Set();
for (const pattern of await lintPartitions(cwd)) {
  const matched = (await files([pattern], false)).sort();
  for (const file of matched) {
    assert(!selected.has(file), `Duplicate partition file: ${file}`);
    selected.add(file);
  }
  partitions.push({ pattern, files: matched });
}
assert(original.length > 0);
assert.deepEqual([...selected].sort(), original);
const baseline = new ESLint({ cwd, overrideConfigFile: configFile });
const current = new ESLint({ cwd, overrideConfigFile: configFile, errorOnUnmatchedPattern: false });
const hash = (bytes) => createHash('sha256').update(bytes).digest('hex');
const configs = {};
for (const file of original) {
  // JSON serialization uses the same calculated-config representation as --print-config.
  const before = JSON.stringify(await baseline.calculateConfigForFile(file));
  const after = JSON.stringify(await current.calculateConfigForFile(file));
  assert(before && before === after, file);
  configs[file] = hash(before);
}
const ignored = [
  'apps/console/dist/lint-proof.tsx',
  'packages/api/src/gen/lint-proof.ts',
  'node_modules/lint-proof.js',
  'apps/portal/test-results/lint-proof.ts',
  'apps/console/playwright-report/lint-proof.js',
];
for (const file of ignored) {
  assert.equal(await baseline.calculateConfigForFile(file), undefined, file);
  assert.equal(await current.calculateConfigForFile(file), undefined, file);
}
const pinned = Object.fromEntries([
  'bin/eslint.js', 'lib/eslint/eslint.js', 'lib/eslint/eslint-helpers.js',
  'lib/config/config-loader.js', 'lib/services/warning-service.js', 'package.json',
].map((path) => [resolve(root, path), hash(readFileSync(resolve(root, path)))]));
console.log(JSON.stringify({
  eslint: ESLint.version, cwd, originalPattern: '.', originalFiles: original,
  partitions, unionCount: selected.size, exactDisjointUnion: true,
  calculatedConfigSha256: configs, allCalculatedConfigsEqual: true,
  ignoredProbes: ignored, pinnedDiscoverySourceSha256: pinned,
  lintExecuted: false,
}, null, 2));
