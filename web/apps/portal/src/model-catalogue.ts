// What a person can read about the models their workspace may call: the names the gateway serves,
// joined to the deployment and model behind each (docs/research/portal-model-catalogue.md).
import type { Deployment, ModelAlias, ModelVersion } from '@ownsolid/api';

/** A name a person may call, and the model behind it when that could be read. */
export interface CatalogueEntry {
  alias: ModelAlias;
  model: ModelVersion | undefined;
}

/** The tiles above the list. */
export const catalogueTiles = ['all', 'callable', 'unavailable'] as const;

export type CatalogueTile = (typeof catalogueTiles)[number];

/**
 * One entry per name that can still be called. A disabled name never can, so it is left out; one
 * whose deployment or model could not be read keeps its row with `model` undefined.
 */
export function catalogueEntries(
  aliases: readonly ModelAlias[],
  deployments: readonly Deployment[],
  models: readonly ModelVersion[],
): CatalogueEntry[] {
  const modelOf = new Map(deployments.map((item) => [item.id, item.model_id]));
  const byId = new Map(models.map((item) => [item.id, item]));
  return aliases
    .filter((alias) => alias.disabled_at === null)
    .map((alias) => {
      const id = modelOf.get(alias.deployment_id);
      return { alias, model: id === undefined ? undefined : byId.get(id) };
    });
}

/** Whether an entry belongs under a tile. */
export function inCatalogueTile(entry: CatalogueEntry, tile: CatalogueTile): boolean {
  if (tile === 'all') return true;
  return entry.alias.available === (tile === 'callable');
}

/** Whether an entry matches what was typed in the search field, already lower-cased. */
export function matchesCatalogueTerm(entry: CatalogueEntry, term: string): boolean {
  return (
    term === '' ||
    entry.alias.name.toLowerCase().includes(term) ||
    (entry.model?.name.toLowerCase().includes(term) ?? false)
  );
}

/** How many tokens of context a model takes. */
export function contextTokens(model: ModelVersion): number {
  const { manifest } = model;
  return 'max_context' in manifest ? manifest.max_context : manifest.maximum_context_tokens;
}

/** The published model another was fine-tuned from, or its own name when it was published. */
export function sourceRepository(model: ModelVersion): string {
  const { manifest } = model;
  return 'repository' in manifest ? manifest.repository : manifest.base_repository;
}
