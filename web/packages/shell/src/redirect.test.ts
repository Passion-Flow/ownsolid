import { describe, expect, it } from 'vitest';
import { safeRedirect } from './redirect.ts';

describe('safeRedirect', () => {
  it.each([
    ['/', '/'],
    ['/nodes', '/nodes'],
    ['/nodes?status=online#row-3', '/nodes?status=online#row-3'],
  ])('keeps the local path %s', (target, expected) => {
    expect(safeRedirect(target)).toBe(expected);
  });

  it.each([
    ['an absolute URL', 'https://attacker.example/'],
    ['a protocol-relative URL', '//attacker.example/'],
    ['a backslash path', '/\\attacker.example'],
    ['a tab hidden in the host', '/\t/attacker.example'],
    ['a newline hidden in the host', '/\n/attacker.example'],
    ['a relative path', 'nodes'],
    ['the sign-in page', '/sign-in?redirect=/nodes'],
    ['a non-string', 42],
    ['nothing', undefined],
  ])('sends %s to the start page', (_name, target) => {
    expect(safeRedirect(target)).toBe('/');
  });
});
