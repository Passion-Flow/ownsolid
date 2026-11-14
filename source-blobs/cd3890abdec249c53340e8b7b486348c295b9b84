import { mkdtempSync, readFileSync, rmSync, writeFileSync, existsSync, mkdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { brotliDecompressSync, gunzipSync } from 'node:zlib';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { precompress } from './precompress.ts';

let directory: string;
const text = 'export const value = "the same words, again and again";\n'.repeat(200);

beforeEach(() => {
  directory = mkdtempSync(join(tmpdir(), 'precompress-'));
  mkdirSync(join(directory, 'assets'));
});
afterEach(() => {
  rmSync(directory, { recursive: true, force: true });
});

describe('precompress', () => {
  it('writes Brotli and gzip copies that decode to the file', () => {
    const file = join(directory, 'assets', 'app-1a2b3c.js');
    writeFileSync(file, text);
    const copies = precompress(directory);
    expect(copies.map((copy) => copy.encoding).sort()).toEqual(['br', 'gzip']);
    expect(brotliDecompressSync(readFileSync(`${file}.br`)).toString()).toBe(text);
    expect(gunzipSync(readFileSync(`${file}.gz`)).toString()).toBe(text);
    for (const copy of copies) expect(copy.bytes).toBeLessThan(copy.originalBytes);
  });

  it('covers the page itself and nested folders', () => {
    writeFileSync(join(directory, 'index.html'), `<!doctype html>${text}`);
    mkdirSync(join(directory, 'assets', 'nested'));
    writeFileSync(join(directory, 'assets', 'nested', 'style.css'), text);
    precompress(directory);
    expect(existsSync(join(directory, 'index.html.br'))).toBe(true);
    expect(existsSync(join(directory, 'assets', 'nested', 'style.css.gz'))).toBe(true);
  });

  it('leaves formats that are compressed already, and small files, alone', () => {
    writeFileSync(join(directory, 'assets', 'font.woff2'), text);
    writeFileSync(join(directory, 'assets', 'tiny.js'), 'export {};\n');
    expect(precompress(directory)).toEqual([]);
    expect(existsSync(join(directory, 'assets', 'font.woff2.br'))).toBe(false);
    expect(existsSync(join(directory, 'assets', 'tiny.js.gz'))).toBe(false);
  });

  it('keeps no copy that would be larger than the file', () => {
    const random = Buffer.from(
      Array.from({ length: 4_096 }, (_, index) => (index * 2_654_435_761) % 256),
    );
    writeFileSync(join(directory, 'assets', 'noise.txt'), random);
    const copies = precompress(directory);
    for (const copy of copies) expect(copy.bytes).toBeLessThan(copy.originalBytes);
  });

  it('gives the same bytes for the same input', () => {
    const file = join(directory, 'assets', 'app.js');
    writeFileSync(file, text);
    precompress(directory);
    const first = [readFileSync(`${file}.br`), readFileSync(`${file}.gz`)];
    precompress(directory);
    expect(readFileSync(`${file}.br`).equals(first[0] as Buffer)).toBe(true);
    expect(readFileSync(`${file}.gz`).equals(first[1] as Buffer)).toBe(true);
  });
});
