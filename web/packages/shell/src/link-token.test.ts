import { describe, expect, it } from 'vitest';
import { parseLinkToken } from './link-token.ts';

const token = 'a'.repeat(43);

describe('parseLinkToken', () => {
  it('reads a well-formed token', () => {
    expect(parseLinkToken(`#token=${token}`)).toBe(token);
  });

  it.each([
    ['nothing', ''],
    ['another parameter', '#other=1'],
    ['a short token', '#token=abc'],
    ['a long token', `#token=${token}a`],
    ['characters outside the format', `#token=${'!'.repeat(43)}`],
  ])('refuses %s', (_name, hash) => {
    expect(parseLinkToken(hash)).toBeNull();
  });
});
