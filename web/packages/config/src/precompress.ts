// Precompressed copies of the build. The control plane sends `name.br` or `name.gz` in place
// of `name` when the browser accepts it, so it never compresses on a request and the first
// download is smaller. Node's own zlib makes both, so the build needs nothing extra offline.
import { readFileSync, readdirSync, statSync, writeFileSync } from 'node:fs';
import { extname, join } from 'node:path';
import { brotliCompressSync, constants, gzipSync } from 'node:zlib';
import type { Plugin } from 'vite';

// Text formats. Fonts and images are compressed already and grow when compressed again.
const compressible = new Set(['.js', '.css', '.html', '.svg', '.json', '.txt']);

// Below this a compressed copy saves less than its response headers cost.
const minimumBytes = 1_024;

/** One copy written: the file it stands for and how much smaller it is. */
export interface CompressedCopy {
  file: string;
  encoding: 'br' | 'gzip';
  bytes: number;
  originalBytes: number;
}

const encoders = [
  {
    encoding: 'br',
    suffix: '.br',
    encode: (data: Buffer) =>
      brotliCompressSync(data, {
        params: {
          [constants.BROTLI_PARAM_QUALITY]: constants.BROTLI_MAX_QUALITY,
          [constants.BROTLI_PARAM_SIZE_HINT]: data.length,
        },
      }),
  },
  { encoding: 'gzip', suffix: '.gz', encode: (data: Buffer) => gzipSync(data, { level: 9 }) },
] as const;

function files(directory: string): string[] {
  return readdirSync(directory, { withFileTypes: true }).flatMap((entry) => {
    const path = join(directory, entry.name);
    return entry.isDirectory() ? files(path) : entry.isFile() ? [path] : [];
  });
}

/**
 * Writes a `.br` and a `.gz` copy next to every text file in `directory` large enough to
 * gain, and keeps a copy only when it is smaller than the file. The output depends only on
 * the input, so two builds of the same source are identical.
 */
export function precompress(directory: string): CompressedCopy[] {
  const written: CompressedCopy[] = [];
  for (const file of files(directory)) {
    if (!compressible.has(extname(file)) || statSync(file).size < minimumBytes) continue;
    const data = readFileSync(file);
    for (const { encoding, suffix, encode } of encoders) {
      const compressed = encode(data);
      if (compressed.length >= data.length) continue;
      writeFileSync(file + suffix, compressed);
      written.push({ file, encoding, bytes: compressed.length, originalBytes: data.length });
    }
  }
  return written;
}

/** Precompresses the application's build once Vite has written it. */
export function precompressPlugin(): Plugin {
  let outDir = '';
  return {
    name: 'ownsolid-precompress',
    apply: 'build',
    configResolved(config) {
      outDir = join(config.root, config.build.outDir);
    },
    closeBundle() {
      precompress(outDir);
    },
  };
}
