import { describe, expect, it } from 'vitest';
import { createTranslator, isTranslationKey, placeholders, type Catalog } from './translate.ts';
import { englishCatalog } from './translate.ts';

describe('createTranslator', () => {
  const t = createTranslator('en', englishCatalog);

  it('substitutes placeholders', () => {
    expect(t('welcome.title', { name: 'Ada' })).toBe('Welcome, Ada');
  });

  it('picks the plural form from count and formats the number', () => {
    expect(t('problem.retryAfter', { count: 1 })).toBe('You can try again in 1 second.');
    expect(t('problem.retryAfter', { count: 1200 })).toBe('You can try again in 1,200 seconds.');
  });

  it('formats numbers for the locale', () => {
    const german = createTranslator('de', englishCatalog);
    expect(german('problem.retryAfter', { count: 1200 })).toBe(
      'You can try again in 1.200 seconds.',
    );
  });

  it('leaves a placeholder without a value visible', () => {
    expect(t('welcome.title')).toBe('Welcome, {name}');
  });

  it('falls back to English for a key missing from the catalog', () => {
    const partial = { 'signIn.title': 'Anmelden' } as unknown as Catalog;
    const de = createTranslator('de', partial);
    expect(de('signIn.title')).toBe('Anmelden');
    expect(de('signOut.action')).toBe('Sign out');
  });
});

describe('isTranslationKey', () => {
  it('accepts plain keys and plural bases', () => {
    expect(isTranslationKey('signIn.title')).toBe(true);
    expect(isTranslationKey('problem.retryAfter')).toBe(true);
    expect(isTranslationKey('problem.NOPE.title')).toBe(false);
  });
});

describe('placeholders', () => {
  it('lists placeholder names', () => {
    expect(placeholders('{a} and {b_2}, not { c }')).toEqual(['a', 'b_2']);
  });
});
