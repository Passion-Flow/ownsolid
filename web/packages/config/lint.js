// Keep type graphs in one short-lived ESLint process per workspace.
import { spawn } from 'node:child_process';
import { readdir } from 'node:fs/promises';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const eslintCLI = fileURLToPath(
  new URL('bin/eslint.js', import.meta.resolve('eslint/package.json')),
);

/** Partition the entire tree, including root/container files and future directories. */
export async function lintPartitions(cwd) {
  const partitions = ['*'];
  const directories = (await readdir(cwd, { withFileTypes: true }))
    .filter((entry) => entry.isDirectory())
    .map((entry) => entry.name)
    .sort();
  for (const directory of directories) {
    if (directory === 'apps' || directory === 'packages') {
      partitions.push(`${directory}/*`);
      for (const entry of (await readdir(resolve(cwd, directory), { withFileTypes: true })).sort(
        (a, b) => a.name.localeCompare(b.name),
      )) {
        if (entry.isDirectory()) partitions.push(`${directory}/${entry.name}`);
      }
    } else {
      partitions.push(directory);
    }
  }
  return partitions;
}

function lintPartition(cwd, configFile, partition) {
  return new Promise((accept, reject) => {
    const child = spawn(
      process.execPath,
      [
        eslintCLI,
        '--config',
        configFile,
        '--max-warnings',
        '0',
        '--no-error-on-unmatched-pattern',
        '--format',
        'json',
        '--',
        partition,
      ],
      { cwd, stdio: ['ignore', 'pipe', 'inherit'] },
    );
    let output = '';
    child.stdout.setEncoding('utf8');
    child.stdout.on('data', (chunk) => (output += chunk));
    child.on('error', reject);
    child.on('close', (code, signal) => {
      if (signal || (code !== 0 && code !== 1)) {
        reject(new Error(`ESLint failed for ${partition}: ${signal ?? `exit ${code}`}`));
        return;
      }
      try {
        const results = JSON.parse(output);
        if (!Array.isArray(results)) throw new Error('Expected ESLint JSON results');
        accept({ results, code });
      } catch (error) {
        reject(new Error(`Invalid ESLint output for ${partition}`, { cause: error }));
      }
    });
  });
}

/** Run all partitions serially with the original config, cwd and zero-warning threshold. */
export async function lintWorkspace(cwd, configFile) {
  const results = [];
  const seen = new Set();
  let exitCode = 0;
  for (const partition of await lintPartitions(cwd)) {
    const checked = await lintPartition(cwd, configFile, partition);
    exitCode = Math.max(exitCode, checked.code);
    for (const result of checked.results) {
      if (seen.has(result.filePath)) throw new Error(`Duplicate lint input: ${result.filePath}`);
      seen.add(result.filePath);
      results.push(result);
    }
  }
  if (!results.length) throw new Error('No files matched the complete web lint');
  results.sort((a, b) => (a.filePath < b.filePath ? -1 : a.filePath > b.filePath ? 1 : 0));
  return { results, exitCode };
}

async function main() {
  const cwd = fileURLToPath(new URL('../../', import.meta.url));
  if (resolve(process.cwd()) !== resolve(cwd)) throw new Error('Run pnpm lint from web/');
  const args = process.argv.slice(2);
  if (args.length === 1 && args[0] === '--print-plan') {
    console.log(JSON.stringify(await lintPartitions(cwd), null, 2));
    return;
  }
  if (args.length && !(args.length === 2 && args[0] === '--format')) {
    throw new Error('Use pnpm lint [--format <formatter>] or pnpm lint --print-plan');
  }
  const configFile = 'packages/config/eslint.config.js';
  const { results, exitCode } = await lintWorkspace(cwd, configFile);
  // Load only the formatter in the coordinator, after every typed process has exited.
  const { ESLint } = await import('eslint');
  const formatter = await new ESLint({ cwd, overrideConfigFile: configFile }).loadFormatter(
    args[1] ?? 'stylish',
  );
  const output = await formatter.format(results);
  if (output) process.stdout.write(output.endsWith('\n') ? output : output + '\n');
  process.exitCode = exitCode;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main().catch((error) => {
    console.error(error);
    process.exitCode = 2;
  });
}
