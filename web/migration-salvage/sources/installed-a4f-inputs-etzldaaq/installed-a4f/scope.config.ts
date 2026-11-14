import { defineConfig } from '@playwright/test';
import { lstatSync } from 'node:fs';
import { isAbsolute, join } from 'node:path';
import { liveOptions } from "/Users/passion./Documents/Passion's WorkSpace/Development/lane-web-acceptance-a4f1120d/web/packages/shell/testing/live.ts";
import { readFixture } from './fixture.ts';
if (process.env.OWNSOLID_GW120_FIXTURE_FILE) readFixture(process.env.OWNSOLID_GW120_FIXTURE_FILE);
const fixture = process.env.OWNSOLID_GW120_FIXTURE_FILE;
const output = process.env.OWNSOLID_GW120_OUTPUT_DIR;
if (fixture && (!output || !isAbsolute(output))) throw new Error('Use run.py to create a fresh private evidence directory');
if (output) {
  const info = lstatSync(output);
  if (!info.isDirectory() || info.uid !== process.getuid?.() || (info.mode & 0o077) !== 0) throw new Error('Evidence directory must be owned, regular and private');
}
const destination = output ?? "/Users/passion./.local/state/ownsolid/web-scopes/installed-a4f-inputs-etzldaaq/installed-a4f/discovery-results";
export default defineConfig({ testDir: "/Users/passion./Documents/Passion's WorkSpace/Development/lane-web-acceptance-a4f1120d/web/apps/portal/e2e", testMatch: 'live-workspace-scope.spec.ts', outputDir: join(destination, 'artifacts'), workers: 1, retries: 0, reporter: [['line'], ['json', { outputFile: join(destination, 'report.json') }]], use: { ...liveOptions, trace: 'off', video: 'off', screenshot: 'off' } });
