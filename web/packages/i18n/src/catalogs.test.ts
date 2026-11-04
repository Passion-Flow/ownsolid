// Every shipped catalog must match English key for key and placeholder for placeholder.
import { describe, expect, it } from 'vitest';
import { englishCatalog, placeholders } from './translate.ts';
import { loadCatalog, supportedLocales } from './locale.ts';

const sorted = (values: Iterable<string>) => [...values].sort();

describe.each(supportedLocales.filter((locale) => locale !== 'en'))('%s catalog', (locale) => {
  it('has exactly the English keys', async () => {
    const catalog = await loadCatalog(locale);
    expect(sorted(Object.keys(catalog))).toEqual(sorted(Object.keys(englishCatalog)));
  });

  it('uses the same placeholders as English', async () => {
    const catalog = (await loadCatalog(locale)) as Record<string, string>;
    for (const [key, english] of Object.entries(englishCatalog)) {
      expect({ key, placeholders: sorted(placeholders(catalog[key] ?? '')) }).toEqual({
        key,
        placeholders: sorted(placeholders(english)),
      });
    }
  });

  it('has no empty messages', async () => {
    const catalog = (await loadCatalog(locale)) as Record<string, string>;
    expect(Object.entries(catalog).filter(([, value]) => value.trim() === '')).toEqual([]);
  });
});

describe('English catalog', () => {
  it('has a plural "other" form for every plural key', () => {
    for (const key of Object.keys(englishCatalog)) {
      const match = /^(.*)_(zero|one|two|few|many)$/.exec(key);
      if (match) expect(Object.hasOwn(englishCatalog, `${match[1] ?? ''}_other`), key).toBe(true);
    }
  });
});
