import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { chmod, mkdtemp, rm, symlink, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { validateFixture, readFixture, password } from './fixture.ts';
import { revision } from './bound-source.ts';

const directory = await mkdtemp('/private/tmp/ow-gwi-');
const readyFile = join(directory, 'ready.json');
const passwordFile = join(directory, 'password.json');
const fixtureFile = join(directory, 'fixture.json');
const imageDigest = `sha256:${'a'.repeat(64)}`;
const ready = JSON.stringify({ candidate: revision, imageDigest, schemaVersion: 60, stableTables: 120 });
let passed = 0;
try {
  await writeFile(readyFile, ready, { mode: 0o600 });
  await writeFile(passwordFile, JSON.stringify({ password: 'synthetic input selftest only' }), { mode: 0o600 });
  const valid = {
    sourceSha: revision, imageDigest, schemaVersion: 60, stableTables: 120,
    readyFile, readySha256: createHash('sha256').update(ready).digest('hex'),
    adminOrigin: 'https://admin.ownsolid.test:8443', portalOrigin: 'https://portal.ownsolid.test:8443',
    admin: { email: 'admin@example.invalid', passwordFile }, member: { email: 'member@example.invalid', passwordFile },
    site: 'default', cell: 'default', workspace: '11111111-1111-4111-8111-111111111111', otherWorkspace: '22222222-2222-4222-8222-222222222222',
    alias: '33333333-3333-4333-8333-333333333333', aliasName: 'gw120-fixture', deployment: '44444444-4444-4444-8444-444444444444', fixturePrefix: 'gw120-',
  };
  assert.deepEqual(validateFixture(valid), valid); passed++;
  for (const changed of [
    { ...valid, extra: true }, { ...valid, sourceSha: 'short' }, { ...valid, schemaVersion: 58 },
    { ...valid, stableTables: 138 }, { ...valid, adminOrigin: 'http://admin.ownsolid.test' },
    { ...valid, portalOrigin: valid.adminOrigin }, { ...valid, otherWorkspace: valid.workspace },
    { ...valid, alias: '00000000-0000-0000-0000-000000000000' }, { ...valid, aliasName: 'somebody-elses-model' },
  ]) { assert.throws(() => validateFixture(changed)); passed++; }
  process.env.OWNSOLID_LIVE_SPKI = `${'A'.repeat(43)}=`;
  process.env.OWNSOLID_LIVE_HOST_RULES = 'MAP admin.ownsolid.test 127.0.0.1, MAP portal.ownsolid.test 127.0.0.1';
  await writeFile(fixtureFile, JSON.stringify(valid), { mode: 0o600 });
  assert.equal(readFixture(fixtureFile).sourceSha, revision); passed++;
  await chmod(fixtureFile, 0o644);
  assert.throws(() => readFixture(fixtureFile)); passed++;
  await chmod(fixtureFile, 0o600);
  await writeFile(fixtureFile, JSON.stringify({ ...valid, sourceSha: 'f'.repeat(40) }));
  assert.throws(() => readFixture(fixtureFile)); passed++;
  await writeFile(fixtureFile, JSON.stringify({ ...valid, readySha256: 'f'.repeat(64) }));
  assert.throws(() => readFixture(fixtureFile)); passed++;
  const stale = JSON.stringify({ candidate: '4463bbe628f60486bbdb92c15f72311fb53d076a', imageDigest });
  await writeFile(readyFile, stale);
  await writeFile(fixtureFile, JSON.stringify({ ...valid, readySha256: createHash('sha256').update(stale).digest('hex') }));
  assert.throws(() => readFixture(fixtureFile)); passed++;
  assert.equal(typeof password(passwordFile), 'string'); passed++;
  await chmod(passwordFile, 0o644);
  assert.throws(() => password(passwordFile)); passed++;
  await chmod(passwordFile, 0o600);
  const link = join(directory, 'password-link.json');
  await symlink(passwordFile, link);
  assert.throws(() => password(link)); passed++;
  console.log(`${passed} input guard selftests passed; no browser, server or real credential was used`);
} finally {
  await rm(directory, { recursive: true });
}
