import { defineConfig } from '@playwright/test';
import { lstatSync } from 'node:fs';
import { isAbsolute, join } from 'node:path';
import { liveOptions } from "/Users/passion./Documents/Passion's WorkSpace/Development/lane-web-acceptance-be62f58e/web/packages/shell/testing/live.ts";
import { readFixture } from './fixture.ts';
if (process.env.OWNSOLID_GW120_FIXTURE_FILE) readFixture(process.env.OWNSOLID_GW120_FIXTURE_FILE);
const fixture = process.env.OWNSOLID_GW120_FIXTURE_FILE;
const output = process.env.OWNSOLID_GW120_OUTPUT_DIR;
if (fixture && (!output || !isAbsolute(output))) throw new Error('Use run.py to create a fresh private evidence directory');
if (output) {
  const info = lstatSync(output);
  if (!info.isDirectory() || info.uid !== process.getuid?.() || (info.mode & 0o077) !== 0) throw new Error('Evidence directory must be owned, regular and private');
}
const destination = output ?? "/Users/passion./.local/state/ownsolid/web-scopes/web-consumers-20261113/installed-be62/discovery-results";
export default defineConfig({ testDir: "/Users/passion./.local/state/ownsolid/web-scopes/web-consumers-20261113/installed-be62", testMatch: 'description.spec.ts', outputDir: join(destination, 'artifacts'), workers: 1, retries: 0, reporter: [['line'], ['json', { outputFile: join(destination, 'report.json') }]], use: { ...liveOptions, trace: 'off', video: 'off', screenshot: 'off' } });
