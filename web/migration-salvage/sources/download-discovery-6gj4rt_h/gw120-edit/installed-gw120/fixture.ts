import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { lstatSync, readFileSync } from 'node:fs';
import { isAbsolute } from 'node:path';
import { checkout, revision } from './bound-source.ts';

/** DEL normalizes the active installation's binding into this private client input. */
export interface Fixture {
  sourceSha: string;
  imageDigest: string;
  schemaVersion: 60;
  stableTables: 120;
  readyFile: string;
  readySha256: string;
  adminOrigin: string;
  portalOrigin: string;
  admin: { email: string; passwordFile: string };
  member: { email: string; passwordFile: string };
  site: string;
  cell: string;
  workspace: string;
  otherWorkspace: string;
  alias: string;
  aliasName: string;
  deployment: string;
  fixturePrefix: string;
}

function requireValue(condition: unknown, message: string): asserts condition {
  if (!condition) throw new Error(message);
}

/** Shape validation is independently callable without reading credentials or opening a browser. */
export function validateFixture(value: unknown): Fixture {
  requireValue(value !== null && typeof value === 'object', 'Fixture must be an object');
  const input = value as Record<string, unknown>;
  const keys = ['sourceSha', 'imageDigest', 'schemaVersion', 'stableTables', 'readyFile', 'readySha256', 'adminOrigin', 'portalOrigin', 'admin', 'member', 'site', 'cell', 'workspace', 'otherWorkspace', 'alias', 'aliasName', 'deployment', 'fixturePrefix'];
  requireValue(Object.keys(input).length === keys.length && keys.every(key => Object.hasOwn(input, key)), 'Fixture keys are missing or unknown');
  for (const key of keys.filter(key => !['schemaVersion', 'stableTables', 'admin', 'member'].includes(key))) {
    requireValue(typeof input[key] === 'string' && input[key] !== '', `Fixture ${key} is required`);
  }
  requireValue(/^[0-9a-f]{40}$/.test(String(input.sourceSha)), 'Use the full frozen source SHA');
  requireValue(/^sha256:[0-9a-f]{64}$/.test(String(input.imageDigest)), 'An immutable image digest is required');
  requireValue(input.schemaVersion === 60 && input.stableTables === 120, 'This input requires the new 60/120 installation');
  requireValue(/^[0-9a-f]{64}$/.test(String(input.readySha256)), 'The active ready file digest is required');
  for (const key of ['adminOrigin', 'portalOrigin']) {
    const origin = new URL(String(input[key]));
    requireValue(origin.protocol === 'https:' && origin.origin === input[key] && !origin.username && !origin.password, `${key} must be an HTTPS origin`);
  }
  requireValue(input.adminOrigin !== input.portalOrigin, 'The two entrances must have distinct origins');
  const uuid = /^[0-9a-f]{8}-(?:[0-9a-f]{4}-){3}[0-9a-f]{12}$/;
  for (const key of ['workspace', 'otherWorkspace', 'alias', 'deployment']) requireValue(uuid.test(String(input[key])) && input[key] !== '00000000-0000-0000-0000-000000000000', `${key} must be a nonzero UUID`);
  requireValue(input.workspace !== input.otherWorkspace, 'The refusal workspace must differ');
  requireValue(isAbsolute(String(input.readyFile)), 'Ready file must use an absolute path');
  requireValue(String(input.aliasName).startsWith(String(input.fixturePrefix)), 'Only the explicitly owned fixture alias may be edited');
  for (const key of ['site', 'cell']) requireValue(/^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$/.test(String(input[key])), `${key} is invalid`);
  for (const name of ['admin', 'member']) {
    const actor = input[name] as Record<string, unknown>;
    requireValue(actor !== null && typeof actor === 'object' && Object.keys(actor).length === 2 && typeof actor.email === 'string' && actor.email.includes('@') && typeof actor.passwordFile === 'string' && actor.passwordFile !== '', `${name} requires email and passwordFile only`);
    requireValue(isAbsolute(String(actor.passwordFile)), `${name} passwordFile must be absolute`);
  }
  return input as unknown as Fixture;
}

function privateFile(file: string): void {
  const info = lstatSync(file);
  requireValue(info.isFile() && info.uid === process.getuid?.() && (info.mode & 0o077) === 0, 'Credential and fixture files must be regular, owned and private');
}

/** Verifies the clean bound checkout and hashes the ready input; DEL owns live image/SQL binding. */
export function readFixture(file: string): Fixture {
  privateFile(file);
  const value = validateFixture(JSON.parse(readFileSync(file, 'utf8')) as unknown);
  requireValue(value.sourceSha === revision, 'Fixture source differs from the bound client');
  requireValue(execFileSync('git', ['rev-parse', 'HEAD'], { cwd: checkout, encoding: 'utf8' }).trim() === revision, 'Bound checkout moved');
  requireValue(execFileSync('git', ['status', '--porcelain'], { cwd: checkout, encoding: 'utf8' }).trim() === '', 'Bound checkout must be clean');
  requireValue(lstatSync(value.readyFile).isFile(), 'Ready input must be a regular file');
  requireValue(createHash('sha256').update(readFileSync(value.readyFile)).digest('hex') === value.readySha256, 'Ready input changed');
  const leaves: unknown[] = [];
  function collect(item: unknown): void {
    if (item !== null && typeof item === 'object') for (const child of Object.values(item)) collect(child);
    else leaves.push(item);
  }
  collect(JSON.parse(readFileSync(value.readyFile, 'utf8')) as unknown);
  requireValue(leaves.some(item => typeof item === 'string' && (/^[0-9a-f]{8,40}$/.test(item) && revision.startsWith(item))), 'Ready file does not contain the frozen source');
  requireValue(leaves.includes(value.imageDigest), 'Ready file does not contain the immutable image digest');
  requireValue(Boolean(process.env.OWNSOLID_LIVE_SPKI?.split(',').every(value => /^[A-Za-z0-9+/]{43}=$/.test(value))) && Boolean(process.env.OWNSOLID_LIVE_HOST_RULES), 'Pinned TLS and local host rules are required');
  return value;
}

/** Reads a password only from its protected flat JSON file; never include it in proof output. */
export function password(file: string): string {
  privateFile(file);
  const value = JSON.parse(readFileSync(file, 'utf8')) as { password?: unknown };
  requireValue(typeof value.password === 'string' && value.password !== '', 'Password file is invalid');
  return value.password;
}
