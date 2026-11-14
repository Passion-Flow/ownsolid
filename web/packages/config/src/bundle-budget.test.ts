// @vitest-environment node
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { gzipSync } from 'node:zlib';
import { build } from 'vite';
import { afterEach, describe, expect, it } from 'vitest';
import {
  budgetViolations,
  bundleBudgetPlugin,
  measureBundle,
  summarizeBundle,
  type BundleFile,
} from './bundle-budget.ts';

// Repetitive text compresses to a predictable size, so each file's gzip size is known.
const text = (seed: string, length: number) => seed.repeat(length);
const gz = (source: string) => gzipSync(source).byteLength;

const files: BundleFile[] = [
  {
    fileName: 'index.js',
    type: 'chunk',
    isEntry: true,
    imports: ['vendor.js'],
    css: ['index.css'],
    source: text('a', 900),
  },
  // A shared chunk that imports the entry back: the walk must not loop.
  { fileName: 'vendor.js', type: 'chunk', imports: ['index.js'], source: text('b', 500) },
  {
    fileName: 'page.js',
    type: 'chunk',
    imports: ['vendor.js'],
    css: ['index.css', 'page.css'],
    source: text('c', 300),
  },
  { fileName: 'index.css', type: 'asset', source: text('d', 200) },
  { fileName: 'page.css', type: 'asset', source: text('e', 100) },
  { fileName: 'font.woff2', type: 'asset', source: new Uint8Array(4000) },
];

describe('measureBundle', () => {
  const report = measureBundle(files);

  it('counts the entry, its static imports and their CSS as the first download', () => {
    expect(report.initial.files).toEqual(['index.css', 'index.js', 'vendor.js']);
    expect(report.initial.bytes).toBe(gz(text('a', 900)) + gz(text('b', 500)) + gz(text('d', 200)));
  });

  it('counts a later chunk with only the CSS the first download lacks', () => {
    expect(report.chunks).toEqual([
      { fileName: 'page.js', bytes: gz(text('c', 300)) + gz(text('e', 100)) },
    ]);
  });

  it('leaves fonts and images out', () => {
    expect(report.initial.files).not.toContain('font.woff2');
  });
});

describe('budgetViolations', () => {
  const report = measureBundle(files);

  it('is empty when everything fits', () => {
    expect(budgetViolations(report, { initial: 10_000, chunk: 10_000 })).toEqual([]);
  });

  it('names the first download and the files in it when it is over', () => {
    const [finding] = budgetViolations(report, { initial: 10, chunk: 10_000 });
    expect(finding).toMatch(/^The first download is \d+\.\d kB gzip, over its 0\.0 kB budget/);
    expect(finding).toContain('index.js, ');
  });

  it('names each chunk that is over', () => {
    expect(budgetViolations(report, { initial: 10_000, chunk: 1 })).toEqual([
      expect.stringMatching(/^page\.js is \d+\.\d kB gzip, over the 0\.0 kB chunk budget\.$/),
    ]);
  });
});

describe('summarizeBundle', () => {
  it('reports the first download and the largest later chunk', () => {
    const line = summarizeBundle(measureBundle(files), { initial: 160_000, chunk: 60_000 });
    expect(line).toMatch(
      /^bundle budget: first download \d+\.\d kB of 160\.0 kB gzip; largest later chunk page\.js \d+\.\d kB of 60\.0 kB$/,
    );
  });
});

describe('bundleBudgetPlugin', () => {
  let root = '';

  afterEach(() => {
    if (root) rmSync(root, { recursive: true, force: true });
  });

  // A real build: an entry with a lazily loaded page.
  async function buildWith(budget: { initial: number; chunk: number }) {
    root = mkdtempSync(join(tmpdir(), 'bundle-budget-'));
    writeFileSync(join(root, 'index.html'), '<script type="module" src="/main.js"></script>');
    writeFileSync(
      join(root, 'main.js'),
      "document.body.dataset.ready = 'yes';\nvoid import('./page.js').then((page) => page.show());",
    );
    writeFileSync(join(root, 'page.js'), 'export function show() { document.title = "page"; }');
    return build({
      root,
      logLevel: 'silent',
      configFile: false,
      build: { outDir: join(root, 'dist'), write: false },
      plugins: [bundleBudgetPlugin(budget)],
    });
  }

  it('lets a build that fits pass', async () => {
    await expect(buildWith({ initial: 100_000, chunk: 100_000 })).resolves.toBeDefined();
  });

  it('fails a build whose first download is over budget, with the next step', async () => {
    await expect(buildWith({ initial: 10, chunk: 100_000 })).rejects.toThrow(
      /The first download is .* over its 0\.0 kB budget.*Load the code a page needs with its route/s,
    );
  });

  it('fails a build with a chunk over budget', async () => {
    await expect(buildWith({ initial: 100_000, chunk: 10 })).rejects.toThrow(
      /assets\/page-[\w-]+\.js is .* over the 0\.0 kB chunk budget/,
    );
  });
});
