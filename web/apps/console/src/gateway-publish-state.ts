// What publishing a deployment to the gateway asks of it and of the person: whether the deployment
// can be published now, the name callers use, and the address of a new gateway
// (docs/research/gateway-publishing.md).
import type { Deployment, GatewayInstance, ModelAlias } from '@ownsolid/api';

/** Where the card's reads live, beside the key reads, so a write refreshes what it changed. */
export const gatewayPublishKeys = {
  gateways: (workspace: string) => ['gateway-keys', 'gateways', workspace] as const,
};

/** The longest name the gateway accepts (api/gateway/v1 `ValidAlias`). */
export const aliasMaximumLength = 64;

const aliasPattern = /^[a-z][a-z0-9.-]{0,63}$/;

/** Why a deployment cannot be published now, or `null` when it can. */
export type Unpublishable = 'removed' | 'notRunning' | 'notVerified' | null;

/**
 * Mirrors what the server asks of a deployment before a name can point at it: running as asked,
 * reported fresh, holding its GPU, and its answer checked by a test generation.
 */
export function unpublishable(deployment: Deployment): Unpublishable {
  if (deployment.removed_at !== null || deployment.desired_state === 'removed') return 'removed';
  if (
    deployment.desired_state !== 'running' ||
    deployment.observed_state !== 'running' ||
    deployment.stale ||
    deployment.resource_state !== 'held'
  ) {
    return 'notRunning';
  }
  const observation = deployment.observation;
  if (!observation?.ready || !observation.verification) return 'notVerified';
  return null;
}

/**
 * A name callers can use for a model, made from its display name: lower case, letters, digits,
 * dots and hyphens, starting with a letter. Empty when nothing usable is left.
 */
export function suggestedAliasName(model: string): string {
  const cleaned = model
    .toLowerCase()
    .replace(/[^a-z0-9.-]+/g, '-')
    .replace(/-{2,}/g, '-')
    .replace(/^[^a-z]+/, '')
    .slice(0, aliasMaximumLength)
    .replace(/[.-]+$/, '');
  return aliasPattern.test(cleaned) ? cleaned : '';
}

/** What is wrong with a name typed for callers, or `null` when the gateway accepts it. */
export function aliasNameProblem(name: string): 'empty' | 'start' | 'characters' | null {
  if (name === '') return 'empty';
  if (!/^[a-z]/.test(name)) return 'start';
  return aliasPattern.test(name) ? null : 'characters';
}

/**
 * The canonical HTTPS origin a typed gateway address stands for, or `null` when it is not one.
 * A trailing slash, upper-case host and the default port are normalized away; a path, query,
 * fragment, credentials or another scheme are refused, as the server refuses them.
 */
export function gatewayOrigin(typed: string): string | null {
  const value = typed.trim().replace(/\/$/, '');
  if (value === '' || /[\s\\]/.test(value) || !/^https:\/\//i.test(value)) return null;
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    return null;
  }
  if (
    url.protocol !== 'https:' ||
    url.hostname === '' ||
    url.username !== '' ||
    url.password !== ''
  )
    return null;
  if (url.pathname !== '/' || url.search !== '' || url.hash !== '') return null;
  // A lone "?" or "#" leaves nothing in search or hash but still makes it more than an origin.
  if (/[?#]/.test(value)) return null;
  return url.origin;
}

/** The base URL an OpenAI-compatible client is given for a gateway. */
export function gatewayBaseUrl(origin: string): string {
  return `${origin}/v1`;
}

/** A call that works as typed once `$OWNSOLID_KEY` holds a key for the name. */
export function sampleCall(origin: string, alias: string): string {
  const body = JSON.stringify({ model: alias, messages: [{ role: 'user', content: 'Hello' }] });
  return [
    `curl ${gatewayBaseUrl(origin)}/chat/completions \\`,
    '  -H "Authorization: Bearer $OWNSOLID_KEY" \\',
    '  -H "Content-Type: application/json" \\',
    `  -d '${body}'`,
  ].join('\n');
}

/** The names that currently send callers to one deployment. */
export function publishedNames(
  aliases: readonly ModelAlias[],
  deployment: string,
): readonly ModelAlias[] {
  return aliases.filter(
    (alias) => alias.deployment_id === deployment && alias.disabled_at === null,
  );
}

/** The gateway that serves a node of the workspace now, if one does. */
export function nodeGateway(
  gateways: readonly GatewayInstance[],
  node: string,
): GatewayInstance | undefined {
  return gateways.find((gateway) => gateway.node_id === node && gateway.state === 'active');
}
