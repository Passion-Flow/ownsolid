// Bundle size budget. The build fails when the code a first visit downloads, or any
// chunk a page loads later, grows past its limit, so growth is a reviewed decision.
import { gzipSync } from 'node:zlib';
import type { Plugin } from 'vite';

/** Limits in gzip bytes. Kilobytes here are 1,000 bytes, as Vite reports them. */
export interface BundleBudget {
  /**
   * The JavaScript and CSS a first visit needs before any page renders: the entry
   * chunk, the chunks it imports statically and their CSS. Fonts and images are not
   * counted; they load on use and stay cached.
   */
  initial: number;
  /** Any one chunk loaded later, with CSS of its own that the first visit lacks. */
  chunk: number;
}

/** One output file, reduced to what the budget reads. */
export interface BundleFile {
  fileName: string;
  type: 'chunk' | 'asset';
  /** Set on a chunk the HTML page loads. */
  isEntry?: boolean;
  /** File names of the chunks this chunk imports statically. */
  imports?: readonly string[];
  /** File names of the CSS this chunk brings. */
  css?: readonly string[];
  source: string | Uint8Array;
}

/** Gzip sizes of a build. */
export interface BundleReport {
  initial: { files: readonly string[]; bytes: number };
  /** Every chunk outside the first download, largest first. */
  chunks: readonly { fileName: string; bytes: number }[];
}

function gzipBytes(source: string | Uint8Array): number {
  return gzipSync(source).byteLength;
}

/** Measures what a first visit downloads and what each later chunk adds. */
export function measureBundle(files: readonly BundleFile[]): BundleReport {
  const byName = new Map(files.map((file) => [file.fileName, file]));
  const initial = new Set<string>();
  const visit = (name: string) => {
    const file = byName.get(name);
    if (!file || initial.has(name)) return;
    initial.add(name);
    for (const css of file.css ?? []) initial.add(css);
    for (const imported of file.imports ?? []) visit(imported);
  };
  for (const file of files) if (file.type === 'chunk' && file.isEntry) visit(file.fileName);

  const size = (name: string) => {
    const file = byName.get(name);
    return file ? gzipBytes(file.source) : 0;
  };
  const initialFiles = [...initial].sort();
  const chunks = files
    .filter((file) => file.type === 'chunk' && !initial.has(file.fileName))
    .map((file) => ({
      fileName: file.fileName,
      bytes:
        gzipBytes(file.source) +
        (file.css ?? [])
          .filter((css) => !initial.has(css))
          .reduce((sum, css) => sum + size(css), 0),
    }))
    .sort((a, b) => b.bytes - a.bytes);
  return {
    initial: {
      files: initialFiles,
      bytes: initialFiles.reduce((sum, name) => sum + size(name), 0),
    },
    chunks,
  };
}

function kilobytes(bytes: number): string {
  return `${(bytes / 1000).toFixed(1)} kB`;
}

/** What exceeds the budget, one sentence per finding; empty when everything fits. */
export function budgetViolations(report: BundleReport, budget: BundleBudget): string[] {
  const found: string[] = [];
  if (report.initial.bytes > budget.initial) {
    found.push(
      `The first download is ${kilobytes(report.initial.bytes)} gzip, over its ${kilobytes(budget.initial)} budget (${report.initial.files.join(', ')}).`,
    );
  }
  for (const chunk of report.chunks) {
    if (chunk.bytes > budget.chunk) {
      found.push(
        `${chunk.fileName} is ${kilobytes(chunk.bytes)} gzip, over the ${kilobytes(budget.chunk)} chunk budget.`,
      );
    }
  }
  return found;
}

/** One line for the build log. */
export function summarizeBundle(report: BundleReport, budget: BundleBudget): string {
  const largest = report.chunks[0];
  return (
    `bundle budget: first download ${kilobytes(report.initial.bytes)} of ${kilobytes(budget.initial)} gzip` +
    (largest
      ? `; largest later chunk ${largest.fileName} ${kilobytes(largest.bytes)} of ${kilobytes(budget.chunk)}`
      : '')
  );
}

/** A build plugin that measures the output and fails the build when it is over budget. */
export function bundleBudgetPlugin(budget: BundleBudget): Plugin {
  return {
    name: 'ownsolid-bundle-budget',
    apply: 'build',
    generateBundle(_options, bundle) {
      const files: BundleFile[] = Object.values(bundle).map((output) =>
        output.type === 'chunk'
          ? {
              fileName: output.fileName,
              type: 'chunk',
              isEntry: output.isEntry,
              imports: output.imports,
              css: [...(output.viteMetadata?.importedCss ?? [])],
              source: output.code,
            }
          : { fileName: output.fileName, type: 'asset', source: output.source },
      );
      const report = measureBundle(files);
      const violations = budgetViolations(report, budget);
      if (violations.length > 0) {
        this.error(
          `${violations.join(' ')} Load the code a page needs with its route ` +
            '(lazyRouteComponent), or raise the budget in packages/config/src/vite.ts and say why.',
        );
      }
      this.info(summarizeBundle(report, budget));
    },
  };
}
