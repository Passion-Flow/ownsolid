import { describe, expect, it } from 'vitest';
import en from '../locales/en.json';
import { createTranslator, hasTranslation, placeholders, type Catalog } from './translate.ts';

const englishCatalog: Catalog = en;

describe('createTranslator', () => {
  const t = createTranslator('en', englishCatalog);

  it('substitutes placeholders', () => {
    expect(t('welcome.title', { name: 'Ada' })).toBe('Welcome, Ada');
  });

  it('picks the plural form from count and formats the number', () => {
    expect(t('workspace.truncated', { count: 1 })).toBe('Showing the first 1 workspace');
    expect(t('workspace.truncated', { count: 1200 })).toBe('Showing the first 1,200 workspaces');
  });

  it('formats numbers for the locale', () => {
    const german = createTranslator('de', englishCatalog);
    expect(german('workspace.truncated', { count: 1200 })).toBe(
      'Showing the first 1.200 workspaces',
    );
  });

  it('leaves a placeholder without a value visible', () => {
    expect(t('welcome.title')).toBe('Welcome, {name}');
  });

  it('shows a key the catalog lacks as itself', () => {
    const partial = { 'signIn.title': 'Anmelden' } as unknown as Catalog;
    const de = createTranslator('de', partial);
    expect(de('signIn.title')).toBe('Anmelden');
    expect(de('signOut.action')).toBe('signOut.action');
  });
});

describe('hasTranslation', () => {
  it('accepts plain keys and plural bases', () => {
    expect(hasTranslation(englishCatalog, 'signIn.title')).toBe(true);
    expect(hasTranslation(englishCatalog, 'workspace.truncated')).toBe(true);
    expect(hasTranslation(englishCatalog, 'problem.NOPE.title')).toBe(false);
  });
});

describe('placeholders', () => {
  it('lists placeholder names', () => {
    expect(placeholders('{a} and {b_2}, not { c }')).toEqual(['a', 'b_2']);
  });
});
