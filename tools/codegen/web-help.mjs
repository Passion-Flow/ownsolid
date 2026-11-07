// Generates the in-product help of both entrances into web/packages/help/src/gen: each article
// parsed once into a Markdown syntax tree, and the API reference of each entrance reduced from
// api/openapi/v1 to what a reader needs (docs/research/in-product-help.md). Run from anywhere:
//
//   node tools/codegen/web-help.mjs          write the files
//   node tools/codegen/web-help.mjs --check  fail when they differ from the sources
import { existsSync, mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { dirname, join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..', '..');
const help = join(root, 'web', 'packages', 'help');
const output = join(help, 'src', 'gen');
const contracts = join(root, 'api', 'openapi', 'v1');

// The parser is a build-time dependency of the package it writes into.
const require = createRequire(join(help, 'package.json'));
const { fromMarkdown } = await import(require.resolve('mdast-util-from-markdown'));
const { gfmTable } = await import(require.resolve('micromark-extension-gfm-table'));
const { gfmTableFromMarkdown } = await import(require.resolve('mdast-util-gfm-table'));

/** Locale directories and the locale each one holds. */
const locales = { en: 'en', 'zh-cn': 'zh-CN' };

/** Each entrance and the contract audience it serves. */
const entrances = { console: 'admin', portal: 'portal' };

/** Node types an article may use; anything else, raw HTML and images included, is refused. */
const allowedNodes = new Set([
  'root',
  'heading',
  'paragraph',
  'text',
  'emphasis',
  'strong',
  'inlineCode',
  'code',
  'list',
  'listItem',
  'link',
  'table',
  'tableRow',
  'tableCell',
  'blockquote',
  'thematicBreak',
  'break',
]);

const methods = ['get', 'post', 'put', 'patch', 'delete'];

// Knowledge belongs to DeepParser, a partner's catalog application, and is never presented as an
// ownsolid capability (AGENTS.md section 1), so its inherited contract stays out of the reference.
const unlisted = new Set(['knowledge']);

const fail = (message) => {
  throw new Error(message);
};

/** The visible text of a node. */
const text = (node) =>
  node.type === 'text' || node.type === 'inlineCode'
    ? node.value
    : (node.children ?? []).map(text).join('');

/** A heading's anchor: lowercase words joined by hyphens, any script kept. */
const anchor = (value) =>
  value
    .toLowerCase()
    .replace(/[^\p{L}\p{N}]+/gu, '-')
    .replace(/^-|-$/g, '');

/** Drops source positions and checks every node, so the page can trust the tree's shape. */
function clean(node, where) {
  if (!allowedNodes.has(node.type)) fail(`${where}: ${node.type} is not allowed in an article`);
  if (node.type === 'heading' && node.depth > 3) fail(`${where}: headings go no deeper than ###`);
  if (node.type === 'link') {
    // Help works offline, so a link goes to a page of this entrance or to another article.
    if (!/^(\/[a-z0-9/-]*|help:[a-z0-9-]+(#[\p{L}\p{N}-]+)?)$/u.test(node.url)) {
      fail(`${where}: link ${node.url} is neither a page path nor help:<article>`);
    }
  }
  const out = { type: node.type };
  for (const key of ['value', 'depth', 'ordered', 'start', 'spread', 'url', 'lang', 'align']) {
    if (node[key] !== undefined && node[key] !== null) out[key] = node[key];
  }
  if (node.children) out.children = node.children.map((child) => clean(child, where));
  // Sections are linkable: the page gives each heading this anchor as its id.
  if (node.type === 'heading' && node.depth > 1) out.data = { id: anchor(text(node)) };
  return out;
}

function article(file) {
  const where = relative(root, file);
  const tree = clean(
    fromMarkdown(readFileSync(file, 'utf8'), {
      extensions: [gfmTable()],
      mdastExtensions: [gfmTableFromMarkdown()],
    }),
    where,
  );
  const [title, ...body] = tree.children;
  if (title?.type !== 'heading' || title.depth !== 1) fail(`${where}: start with one # title`);
  if (body.some((node) => node.type === 'heading' && node.depth === 1)) {
    fail(`${where}: only the title is a # heading`);
  }
  const summary = body.find((node) => node.type === 'paragraph');
  if (!summary) fail(`${where}: a paragraph after the title summarizes the article`);
  const sections = body
    .filter((node) => node.type === 'heading' && node.depth === 2)
    .map((node) => ({ title: text(node), anchor: anchor(text(node)) }));
  return {
    entry: { title: text(title), summary: text(summary), sections },
    content: { type: 'root', children: body },
  };
}

function articles(files, index) {
  const manifest = JSON.parse(readFileSync(join(help, 'articles.json'), 'utf8'));
  for (const [entrance, list] of Object.entries(manifest)) {
    if (!(entrance in entrances)) fail(`articles.json: unknown entrance ${entrance}`);
    index.articles[entrance] = list.map(({ slug, topic, pages }) => ({ slug, topic, pages }));
    index.text[entrance] = {};
    for (const [directory, locale] of Object.entries(locales)) {
      const entries = {};
      for (const { slug } of list) {
        // An article about a page both entrances have is written once, in `shared`.
        const own = join(help, 'locales', directory, entrance, `${slug}.md`);
        const file = existsSync(own) ? own : join(help, 'locales', directory, 'shared', `${slug}.md`);
        if (!existsSync(file)) fail(`${relative(root, own)} is missing, and not in shared`);
        const parsed = article(file);
        entries[slug] = parsed.entry;
        // Translated text lives only under locales/ (AGENTS.md section 7).
        files.set(`locales/${directory}/${entrance}/${slug}.json`, json(parsed.content));
      }
      files.set(`locales/${directory}/${entrance}.json`, json(entries));
      index.text[entrance][locale] = `${entrance}_${directory.replace('-', '_')}`;
    }
  }
  // Every file is an article some entrance lists, so nothing written is silently left out.
  for (const directory of Object.keys(locales)) {
    for (const folder of [...Object.keys(entrances), 'shared']) {
      const listed = (entrance) => manifest[entrance].map((item) => item.slug);
      const wanted = folder === 'shared' ? Object.keys(manifest).flatMap(listed) : listed(folder);
      const path = join(help, 'locales', directory, folder);
      const present = existsSync(path)
        ? readdirSync(path).map((name) => name.replace(/\.md$/, ''))
        : [];
      const extra = present.filter((slug) => !wanted.includes(slug));
      if (extra.length > 0) fail(`${directory}/${folder}: ${extra.join(', ')} not in articles.json`);
    }
  }
}

/** The parts of a schema the reference shows; the problem envelope is described once, by code. */
function schema(value) {
  if (value === null || typeof value !== 'object') return value;
  if (Array.isArray(value)) return value.map(schema);
  const out = {};
  for (const key of [
    '$ref',
    'type',
    'format',
    'description',
    'enum',
    'const',
    'required',
    'minimum',
    'maximum',
    'minLength',
    'maxLength',
    'maxItems',
  ]) {
    if (value[key] !== undefined) out[key] = value[key];
  }
  if (value.$ref) out.$ref = value.$ref.replace('#/components/schemas/', '');
  if (value.properties) {
    out.properties = Object.fromEntries(
      Object.entries(value.properties).map(([name, property]) => [name, schema(property)]),
    );
  }
  if (value.items) out.items = schema(value.items);
  for (const key of ['anyOf', 'oneOf', 'allOf']) if (value[key]) out[key] = value[key].map(schema);
  return out;
}

/** The schemas an operation reaches, by name, following references. */
function reach(value, all, found) {
  if (value === null || typeof value !== 'object') return;
  if (Array.isArray(value)) {
    for (const item of value) reach(item, all, found);
    return;
  }
  if (typeof value.$ref === 'string') {
    const name = value.$ref.replace('#/components/schemas/', '');
    if (name !== 'Problem' && !found.has(name) && all[name]) {
      found.add(name);
      reach(all[name], all, found);
    }
  }
  for (const child of Object.values(value)) reach(child, all, found);
}

function api(files, index) {
  const domains = readdirSync(contracts, { withFileTypes: true })
    .filter((entry) => entry.isDirectory())
    .map((entry) => entry.name)
    .filter((name) => existsSync(join(contracts, name, 'openapi.json')) && !unlisted.has(name))
    .sort();
  for (const [entrance, audience] of Object.entries(entrances)) {
    index.api[entrance] = [];
    for (const domain of domains) {
      const contract = JSON.parse(readFileSync(join(contracts, domain, 'openapi.json'), 'utf8'));
      const all = contract.components?.schemas ?? {};
      const operations = [];
      const used = new Set();
      for (const [path, item] of Object.entries(contract.paths ?? {})) {
        for (const method of methods) {
          const operation = item[method];
          if (!operation?.['x-audiences']?.includes(audience)) continue;
          const parameters = [...(item.parameters ?? []), ...(operation.parameters ?? [])]
            // Origin and CSRF headers are the browser's business, not the caller's.
            .filter((parameter) => parameter.in !== 'header' && parameter.in !== 'cookie')
            .map((parameter) => ({
              name: parameter.name,
              in: parameter.in,
              required: parameter.required === true,
              schema: schema(parameter.schema ?? {}),
              ...(parameter.description ? { description: parameter.description } : {}),
            }));
          const body = operation.requestBody?.content?.['application/json']?.schema;
          const responses = Object.entries(operation.responses ?? {})
            .filter(([status]) => /^2\d\d$/.test(status))
            .map(([status, response]) => ({
              status,
              schema: schema(response.content?.['application/json']?.schema ?? null),
            }));
          reach({ parameters, body, responses }, all, used);
          operations.push({
            id: operation.operationId ?? `${method} ${path}`,
            method,
            path,
            parameters,
            body: body ? schema(body) : null,
            responses,
            errors: operation['x-error-codes'] ?? {},
          });
        }
      }
      if (operations.length === 0) continue;
      const schemas = Object.fromEntries(
        [...used].sort().map((name) => [name, schema(all[name])]),
      );
      // The contract as this entrance serves it, for the reader's own tools to load.
      const served = Object.fromEntries(
        Object.entries(contract.paths ?? {})
          .map(([path, item]) => [
            path,
            Object.fromEntries(
              Object.entries(item).filter(
                ([key, operation]) =>
                  !methods.includes(key) || operation['x-audiences']?.includes(audience),
              ),
            ),
          ])
          .filter(([, item]) => methods.some((method) => method in item)),
      );
      files.set(
        `contracts/${entrance}/${domain}.json`,
        `${JSON.stringify({ ...contract, paths: served })}\n`,
      );
      files.set(
        `api/${entrance}/${domain}.json`,
        json({ title: contract.info?.title ?? domain, operations, schemas }),
      );
      index.api[entrance].push({
        domain,
        operations: operations.map(({ id, method, path }) => ({ id, method, path })),
      });
    }
  }
}

const json = (value) => `${JSON.stringify(value, null, 1)}\n`;

function generate() {
  const files = new Map();
  const index = { articles: {}, text: {}, api: {} };
  articles(files, index);
  api(files, index);
  // Each language's listing is imported by name, so the index itself holds no translated text.
  const imports = Object.entries(index.text).flatMap(([entrance, byLocale]) =>
    Object.entries(locales).map(
      ([directory, locale]) =>
        `import ${byLocale[locale]} from './locales/${directory}/${entrance}.json' with { type: 'json' };`,
    ),
  );
  const text = Object.entries(index.text)
    .map(
      ([entrance, byLocale]) =>
        ` ${entrance}: { ${Object.entries(byLocale)
          .map(([locale, name]) => `'${locale}': ${name}`)
          .join(', ')} },`,
    )
    .join('\n');
  const { articles: manifest, api: reference } = index;
  files.set(
    'index.ts',
    '// Generated by tools/codegen/web-help.mjs from web/packages/help/locales and api/openapi/v1.\n' +
      '// Do not edit: change the articles or contracts, then run `make generate`.\n' +
      "import type { HelpIndex } from '../types.ts';\n" +
      `${imports.join('\n')}\n\n` +
      `export const helpIndex: HelpIndex = {\n articles: ${JSON.stringify(manifest)},\n` +
      ` text: {\n${text}\n },\n api: ${JSON.stringify(reference)},\n};\n`,
  );
  return files;
}

function existing(directory, prefix = '') {
  if (!existsSync(directory)) return new Map();
  const out = new Map();
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    const name = `${prefix}${entry.name}`;
    if (entry.isDirectory()) {
      for (const [key, value] of existing(join(directory, entry.name), `${name}/`)) {
        out.set(key, value);
      }
    } else {
      out.set(name, readFileSync(join(directory, entry.name), 'utf8'));
    }
  }
  return out;
}

const files = generate();
if (process.argv.includes('--check')) {
  const current = existing(output);
  const stale = [...new Set([...files.keys(), ...current.keys()])].filter(
    (name) => files.get(name) !== current.get(name),
  );
  if (stale.length > 0) {
    console.error(`web/packages/help/src/gen is stale; run \`make generate\`:\n${stale.join('\n')}`);
    process.exit(1);
  }
} else {
  rmSync(output, { recursive: true, force: true });
  for (const [name, content] of files) {
    mkdirSync(dirname(join(output, name)), { recursive: true });
    writeFileSync(join(output, name), content);
  }
  console.log(`wrote ${String(files.size)} files to web/packages/help/src/gen`);
}
