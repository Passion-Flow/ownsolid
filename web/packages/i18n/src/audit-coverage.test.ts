// Every action a person can meet in the audit trail reads as words in both languages (UX-108).
// The families below have pages, so a new action of theirs fails here until it is named. Other
// families show their family name and identifier until their page ships and joins this list.
import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import chinese from '../locales/zh-cn.json' with { type: 'json' };
import english from '../locales/en.json' with { type: 'json' };

const root = join(import.meta.dirname, '..', '..', '..', '..');
const server = join(root, 'services', 'control', 'internal');

const named = [
  'account',
  'audit',
  'auth',
  'gateway',
  'installation',
  'invitation',
  'license',
  'mail',
  'node',
  'operator',
  'preference',
  'sso',
  'tenant',
  'workspace',
];

// Shaped like an action, but a configuration path or an outbox topic.
const notActions = new Set(['sso.keyring_file', 'node.view_changed', 'node.views_changed']);

function sources(directory: string, found: string[] = []): string[] {
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    if (entry.isDirectory()) sources(path, found);
    else if (entry.name.endsWith('.go') && !entry.name.endsWith('_test.go')) found.push(path);
  }
  return found;
}

const pattern = new RegExp(`["'\`]((?:${named.join('|')})\\.[a-z_]+)["'\`]`, 'g');
const actions = new Set<string>();
for (const file of sources(server)) {
  for (const match of readFileSync(file, 'utf8').matchAll(pattern)) {
    const action = match[1] ?? '';
    if (!action.endsWith('_') && !notActions.has(action)) actions.add(action);
  }
}

describe('audit action names', () => {
  it('finds the actions the server records', () => {
    expect(actions.size).toBeGreaterThan(50);
    expect(actions.has('auth.login_succeeded')).toBe(true);
  });

  it.each([
    ['English', english as Record<string, string>],
    ['Chinese', chinese as Record<string, string>],
  ])('names every action of a module with a page in %s', (_, catalog) => {
    const missing = [...actions].sort().filter((action) => !catalog[`audit.action.${action}`]);
    expect(missing).toEqual([]);
  });
});
