// The catalogs say what the declaration says: English word for word, because the installer and API
// clients read the declaration itself, and every other language the same keys. A row added to
// pkg/capabilities/declaration.json fails here until both languages carry it.
import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import chinese from '../locales/zh-cn.json' with { type: 'json' };
import english from '../locales/en.json' with { type: 'json' };

interface Declaration {
  registries: {
    id: string;
    name: string;
    description: string;
    options: { id: string; name: string; reason?: string; note?: string }[];
  }[];
}

const root = join(import.meta.dirname, '..', '..', '..', '..');
const declaration = JSON.parse(
  readFileSync(join(root, 'pkg', 'capabilities', 'declaration.json'), 'utf8'),
) as Declaration;

const declared = new Map<string, string>();
for (const registry of declaration.registries) {
  declared.set(`capability.${registry.id}.title`, registry.name);
  declared.set(`capability.${registry.id}.description`, registry.description);
  for (const option of registry.options) {
    const base = `capability.${registry.id}.${option.id}`;
    declared.set(`${base}.name`, option.name);
    if (option.reason) declared.set(`${base}.reason`, option.reason);
    if (option.note) declared.set(`${base}.note`, option.note);
  }
}

const catalogKeys = (catalog: Record<string, string>) =>
  Object.keys(catalog).filter((key) => {
    const [, group] = key.split('.');
    return (
      key.startsWith('capability.') &&
      !['page', 'state', 'filter', 'search', 'column', 'empty'].includes(group ?? '')
    );
  });

describe('capability text', () => {
  it('in English is the declaration, word for word', () => {
    const catalog: Record<string, string> = english;
    expect(Object.fromEntries(catalogKeys(catalog).map((key) => [key, catalog[key]]))).toEqual(
      Object.fromEntries(declared),
    );
  });

  it('in Chinese has every key and differs where a translation is due', () => {
    const catalog: Record<string, string> = chinese;
    expect(catalogKeys(catalog).toSorted()).toEqual([...declared.keys()].toSorted());
    for (const [key, text] of declared) {
      if (key.endsWith('.reason') || key.endsWith('.note') || key.endsWith('.description')) {
        expect(catalog[key], key).not.toBe(text);
      }
    }
  });
});
