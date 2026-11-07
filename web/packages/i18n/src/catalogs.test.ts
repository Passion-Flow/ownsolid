// Every shipped catalog must match English key for key and placeholder for placeholder.
import { describe, expect, it } from 'vitest';
import en from '../locales/en.json';
import { placeholders, type Catalog } from './translate.ts';
import { loadCatalog, supportedLocales } from './locale.ts';

const englishCatalog: Catalog = en;

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

// One word for one thing across the Chinese product text, help and email: the glossary in
// locales/glossary-zh-cn.md. Unicode test input is written with escapes.
const notInGlossary = [
  '\u8d26\u6237', // account: the glossary says \u8d26\u53f7
  '\u60a8', // you: the product says \u4f60
  '\u767b\u51fa', // signed out: the glossary says \u9000\u51fa\u767b\u5f55
  '\u5de5\u4f5c\u533a', // workspace: the glossary says \u5de5\u4f5c\u7a7a\u95f4
  '\u7535\u5b50\u90ae\u4ef6', // email: the glossary says \u90ae\u4ef6
];

describe('Chinese glossary', () => {
  it('uses one word for one thing', async () => {
    const catalog = (await loadCatalog('zh-CN')) as Record<string, string>;
    const stray = Object.entries(catalog).filter(([, value]) =>
      notInGlossary.some((word) => value.includes(word)),
    );
    expect(stray).toEqual([]);
  });

  it('uses Chinese punctuation next to Chinese text', async () => {
    const catalog = (await loadCatalog('zh-CN')) as Record<string, string>;
    const stray = Object.entries(catalog).filter(([, value]) =>
      /[\u4e00-\u9fff][,.:;?!()]|[,:;?!(][\u4e00-\u9fff]/.test(value),
    );
    expect(stray).toEqual([]);
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
