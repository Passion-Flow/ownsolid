import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { mkdtemp, mkdir, rm, symlink, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import { test } from 'node:test';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';
import { lintPartitions, lintWorkspace } from './lint.js';

const execute = promisify(execFile);
const cli = fileURLToPath(new URL('bin/eslint.js', import.meta.resolve('eslint/package.json')));
const config = `export default [
  { ignores: ['**/dist/', '**/node_modules/', '**/gen/', '**/test-results/', '**/playwright-report/'] },
  { files: ['**/*.js', '**/*.mjs'], rules: { 'no-undef': 'error' } }
];`;

async function fixture(t, files) {
  const cwd = await mkdtemp(join(tmpdir(), 'ownsolid-lint-'));
  t.after(() => rm(cwd, { recursive: true, force: true }));
  for (const [path, content] of Object.entries({ 'eslint.config.mjs': config, ...files })) {
    await mkdir(dirname(join(cwd, path)), { recursive: true });
    await writeFile(join(cwd, path), content);
  }
  return cwd;
}

async function original(cwd) {
  try {
    const output = await execute(
      process.execPath,
      [
        cli,
        '--config',
        join(cwd, 'eslint.config.mjs'),
        '--max-warnings',
        '0',
        '--format',
        'json',
        '.',
      ],
      { cwd },
    );
    return { results: JSON.parse(output.stdout), exitCode: 0 };
  } catch (error) {
    if (error.code === 1) return { results: JSON.parse(error.stdout), exitCode: 1 };
    throw error;
  }
}

function sorted(results) {
  return [...results].sort((a, b) => a.filePath.localeCompare(b.filePath));
}

test('covers root, containers, every workspace and new hidden/non-workspace directories', async (t) => {
  const cwd = await fixture(t, {
    'root.js': 'export const root = 1;',
    'apps/container.js': 'export const container = 1;',
    'apps/console/src/page.js': 'export const page = 1;',
    'apps/console/e2e/page.js': 'export const e2e = 1;',
    'apps/portal/src/page.js': 'export const portal = 1;',
    'packages/container.js': 'export const packages = 1;',
    'packages/config/settings.js': 'export const settings = 1;',
    'packages/shell/testing/fixture.js': 'export const fixture = 1;',
    'packages/new-package/src/new.js': 'export const added = 1;',
    '.extra/hidden.js': 'export const hidden = 1;',
    'future/shared.js': 'export const future = 1;',
    'apps/console/dist/ignored.js': 'undefinedBuild;',
    'packages/api/src/gen/ignored.js': 'undefinedGenerated;',
    'node_modules/ignored.js': 'undefinedDependency;',
    'apps/portal/test-results/ignored.js': 'undefinedResult;',
    'apps/console/playwright-report/ignored.js': 'undefinedReport;',
    'unknown.txt': 'not JavaScript',
  });
  const before = await original(cwd);
  const after = await lintWorkspace(cwd, 'eslint.config.mjs');
  assert.equal(before.results.length, 12);
  assert.deepEqual(sorted(after.results), sorted(before.results));
  assert.equal(after.exitCode, 0);
});

test('matches original diagnostics and rejects errors in root, e2e, shared and config files', async (t) => {
  const cwd = await fixture(t, {
    'root.js': 'unknownRoot;',
    'apps/console/e2e/check.js': 'unknownE2E;',
    'packages/shell/testing/fixture.js': 'unknownShared;',
    'packages/config/settings.js': 'unknownConfig;',
  });
  const before = await original(cwd);
  const after = await lintWorkspace(cwd, 'eslint.config.mjs');
  assert.equal(after.exitCode, 1);
  assert.equal(
    after.results.reduce((sum, result) => sum + result.errorCount, 0),
    4,
  );
  assert.deepEqual(sorted(after.results), sorted(before.results));
});

test('fails on a warning even when no partition has an error', async (t) => {
  const cwd = await fixture(t, {
    'packages/example/warning.js': '/* eslint no-undef: warn */\nundefinedWarning;',
  });
  const before = await original(cwd);
  const after = await lintWorkspace(cwd, 'eslint.config.mjs');
  assert.equal(after.exitCode, 1);
  assert.equal(
    after.results.reduce((sum, result) => sum + result.warningCount, 0),
    1,
  );
  assert.deepEqual(sorted(after.results), sorted(before.results));
});

test('retains fatal syntax diagnostics and a failing exit', async (t) => {
  const cwd = await fixture(t, { 'apps/example/broken.js': 'export const = ;' });
  const before = await original(cwd);
  const after = await lintWorkspace(cwd, 'eslint.config.mjs');
  assert.equal(after.exitCode, 1);
  assert.equal(
    after.results.reduce((sum, result) => sum + result.fatalErrorCount, 0),
    1,
  );
  assert.deepEqual(sorted(after.results), sorted(before.results));
});

test('does not replace an all-ignored original failure with a successful empty lint', async (t) => {
  const cwd = await fixture(t, { 'eslint.config.mjs': 'export default [{ ignores: ["**"] }];' });
  await assert.rejects(original(cwd));
  await assert.rejects(lintWorkspace(cwd, 'eslint.config.mjs'), /No files matched/);
});

test('refuses malformed configuration and a terminated lint process', async (t) => {
  const cwd = await fixture(t, {
    'eslint.config.mjs': 'export default [{ rules: { missing: 2 } }];',
  });
  await assert.rejects(lintWorkspace(cwd, 'eslint.config.mjs'), /ESLint failed/);
  await writeFile(join(cwd, 'eslint.config.mjs'), 'process.kill(process.pid, "SIGTERM");');
  await assert.rejects(lintWorkspace(cwd, 'eslint.config.mjs'), /SIGTERM/);
});

test('does not follow directory symlinks when planning partitions', async (t) => {
  const cwd = await fixture(t, { 'apps/console/src/page.js': 'export const value = 1;' });
  await symlink(join(cwd, 'apps'), join(cwd, 'alias'), 'dir');
  assert.deepEqual(await lintPartitions(cwd), ['*', 'apps/*', 'apps/console']);
  const before = await original(cwd);
  const after = await lintWorkspace(cwd, 'eslint.config.mjs');
  assert.deepEqual(sorted(after.results), sorted(before.results));
});
