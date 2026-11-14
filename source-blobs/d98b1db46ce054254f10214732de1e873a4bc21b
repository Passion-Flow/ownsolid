import { afterEach, describe, expect, it, vi } from 'vitest';
import { sha256Hex } from './sha256.ts';

const empty = 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855';
const abc = 'ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad';
const bytes = (text: string) => new TextEncoder().encode(text).buffer;

describe('sha256Hex', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('gives the published digests through Web Crypto', async () => {
    expect(await sha256Hex(bytes(''))).toBe(empty);
    expect(await sha256Hex(bytes('abc'))).toBe(abc);
  });

  it('gives the same digests where Web Crypto is missing, as on plain HTTP', async () => {
    vi.stubGlobal('crypto', {});
    expect(await sha256Hex(bytes(''))).toBe(empty);
    expect(await sha256Hex(bytes('abc'))).toBe(abc);
  });
});
