import { describe, expect, it } from 'vitest';
import { isLocale, negotiateLocale } from './locale.ts';

describe('negotiateLocale', () => {
  it.each([
    [['zh-CN'], 'zh-CN'],
    [['zh-cn'], 'zh-CN'],
    [['zh-Hans-CN', 'en'], 'zh-CN'],
    [['zh-TW'], 'zh-CN'],
    [['en-GB'], 'en'],
    [['fr-FR', 'zh-CN'], 'zh-CN'],
    [['fr-FR'], 'en'],
    [[], 'en'],
  ])('%j picks %s', (preferred, expected) => {
    expect(negotiateLocale(preferred)).toBe(expected);
  });
});

describe('isLocale', () => {
  it('accepts only exact supported tags', () => {
    expect(isLocale('zh-CN')).toBe(true);
    expect(isLocale('zh-cn')).toBe(false);
    expect(isLocale(undefined)).toBe(false);
  });
});
