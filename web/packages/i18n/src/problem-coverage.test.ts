// Every failure the console can meet is explained to the person: what happened and what to
// do next (UX-107). The calls come from the API client's source, their codes from the
// contracts, so a new call or a new code fails here until both languages explain it.
import { readFileSync, readdirSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import chinese from '../locales/zh-cn.json' with { type: 'json' };
import english from '../locales/en.json' with { type: 'json' };

const root = join(import.meta.dirname, '..', '..', '..', '..');
const clientSource = join(root, 'web', 'packages', 'api', 'src');
const contracts = join(root, 'api', 'openapi', 'v1');

// Problems the client raises itself, and the fallback for a code it does not know.
const clientCodes = ['NETWORK_UNAVAILABLE', 'REQUEST_TIMEOUT', 'RESPONSE_INVALID', 'UNKNOWN'];

interface Operation {
  'x-error-codes'?: Record<string, string[]>;
}

function calls(): { method: string; path: string }[] {
  const found: { method: string; path: string }[] = [];
  for (const file of readdirSync(clientSource)) {
    if (!file.endsWith('.ts') || file.endsWith('.test.ts')) continue;
    const text = readFileSync(join(clientSource, file), 'utf8');
    for (const match of text.matchAll(/\.(get|post|put|patch|delete)\(\s*'(\/[^']+)'/g)) {
      found.push({ method: match[1] ?? '', path: match[2] ?? '' });
    }
  }
  return found;
}

function operations(): Map<string, Operation> {
  const all = new Map<string, Operation>();
  for (const domain of readdirSync(contracts)) {
    let raw: string;
    try {
      raw = readFileSync(join(contracts, domain, 'openapi.json'), 'utf8');
    } catch {
      continue;
    }
    const contract = JSON.parse(raw) as { paths: Record<string, Record<string, Operation>> };
    for (const [path, methods] of Object.entries(contract.paths)) {
      for (const [method, operation] of Object.entries(methods)) {
        all.set(`${method} ${path}`, operation);
      }
    }
  }
  return all;
}

const used = calls();
const known = operations();
const codes = new Set(clientCodes);
for (const call of used) {
  const declared = known.get(`${call.method} ${call.path}`)?.['x-error-codes'] ?? {};
  for (const list of Object.values(declared)) for (const code of list) codes.add(code);
}

describe('error explanations', () => {
  it('finds the calls the console makes', () => {
    expect(used.length).toBeGreaterThan(20);
  });

  it('knows every call and the failures it can meet', () => {
    const undeclared = used
      .map((call) => `${call.method} ${call.path}`)
      .filter((key) => !known.get(key)?.['x-error-codes']);
    expect(undeclared).toEqual([]);
  });

  it.each([
    ['English', english as Record<string, string>],
    ['Chinese', chinese as Record<string, string>],
  ])('explains every failure in %s, with what happened and what to do', (_, catalog) => {
    const missing = [...codes]
      .sort()
      .filter((code) => !catalog[`problem.${code}.title`] || !catalog[`problem.${code}.detail`]);
    expect(missing).toEqual([]);
  });
});
