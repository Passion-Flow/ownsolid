// Resolve the supported local path aliases before Web contract projection.
import { readFileSync } from 'node:fs';
import { basename, dirname, join } from 'node:path';

const method = /^(get|put|post|delete|options|head|patch|trace)$/;

export function readContract(source) {
  const document = JSON.parse(readFileSync(source, 'utf8'));
  const directory = dirname(source);
  let owner;
  let hasAliases = false;
  for (const [path, item] of Object.entries(document.paths ?? {})) {
    if (!Object.hasOwn(item, '$ref')) continue;
    const pointer = encodeURIComponent(path.replaceAll('~', '~0').replaceAll('/', '~1'));
    const expected = '../components/openapi.json#/paths/' + pointer;
    if (basename(directory) !== 'nodes' || item.$ref !== expected || Object.keys(item).length !== 1) {
      throw new Error(source + ': unsupported OpenAPI path reference for ' + path);
    }
    owner ??= JSON.parse(readFileSync(join(directory, '..', 'components', 'openapi.json'), 'utf8'));
    const canonical = owner.paths?.[path];
    if (!canonical || Object.hasOwn(canonical, '$ref') || !Object.keys(canonical).some((key) => method.test(key))) {
      throw new Error(source + ': missing canonical OpenAPI path for ' + path);
    }
    document.paths[path] = canonical;
    hasAliases = true;
  }
  return { document, hasAliases };
}
