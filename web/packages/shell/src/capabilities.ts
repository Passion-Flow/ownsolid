// The capability declaration as the entrances read it: one query, and its text in the person's
// language (docs/research/capability-registry.md).
import {
  listCapabilities,
  type CapabilityDeclaration,
  type CapabilityOption,
  type CapabilityRegistry,
} from '@ownsolid/api';
import type { TranslationKey } from '@ownsolid/i18n';
import { queryOptions } from '@tanstack/react-query';

/** The declaration is fixed for a release, so one read serves a whole visit. */
export function capabilitiesQuery() {
  return queryOptions({
    queryKey: ['capabilities'],
    queryFn: ({ signal }): Promise<CapabilityDeclaration> => listCapabilities({ signal }),
    staleTime: 'static',
  });
}

type Translate = (key: TranslationKey) => string;

/** An option's name in the person's language. */
export function capabilityName(
  t: Translate,
  registry: Pick<CapabilityRegistry, 'id'>,
  option: Pick<CapabilityOption, 'id'>,
): string {
  return t(`capability.${registry.id}.${option.id}.name` as TranslationKey);
}

/** Why an option cannot simply be chosen or, when it can, a fact about it. */
export function capabilityDetail(
  t: Translate,
  registry: Pick<CapabilityRegistry, 'id'>,
  option: CapabilityOption,
): string {
  const field = option.reason ? 'reason' : option.note ? 'note' : null;
  return field ? t(`capability.${registry.id}.${option.id}.${field}` as TranslationKey) : '';
}

/** The names of what can be chosen now in one registry, in the person's language. */
export function readyCapabilityNames(
  t: Translate,
  declaration: CapabilityDeclaration | undefined,
  registryId: string,
): string[] {
  const registry = declaration?.registries.find(({ id }) => id === registryId);
  if (!registry) return [];
  return registry.options
    .filter((option) => option.state === 'ready')
    .map((option) => capabilityName(t, registry, option));
}

/** Whether a search finds an option, by what the page shows of it or its English name. */
export function optionMatches(
  search: string,
  option: CapabilityOption,
  shown: readonly string[],
): boolean {
  const term = search.trim().toLowerCase();
  if (term === '') return true;
  return [option.name, option.roadmap_item ?? '', ...shown].some((text) =>
    text.toLowerCase().includes(term),
  );
}
