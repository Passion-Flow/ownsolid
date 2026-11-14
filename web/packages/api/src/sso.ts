// Single sign-on connectors, for platform administrators (api/openapi/v1/sso).
import { createClient } from './client.ts';
import type { ssoContract } from './gen/index.ts';
import type { RequestOptions } from './transport.ts';

type Schemas = ssoContract.components['schemas'];

/** A connector as an administrator sees it. Its client secret never comes back. */
export type ManagedConnector = Schemas['SSOConnector'];

/** One page of connectors, newest first. */
export type ManagedConnectorPage = Schemas['SSOConnectorPageResponse']['data'];

/** What the installation lets single sign-on use. */
export type SsoConfiguration = Schemas['SSOConfiguration'];

/** The result of checking a connector against its provider. */
export type ConnectorCheck = Schemas['SSOConnectorCheck'];

/** What a connector check found. */
export type ConnectorCheckOutcome = ConnectorCheck['outcome'];

const client = createClient<ssoContract.paths>();

/** The callback URL a provider must accept for a connector at one entrance origin. */
export function connectorCallbackUrl(origin: string, connectorId: string): string {
  return `${origin}/api/v1/auth/sso/${encodeURIComponent(connectorId)}/callback`;
}

/** Reads whether single sign-on is on, the approved providers and the entrance origins. */
export function getSsoConfiguration(options?: RequestOptions): Promise<SsoConfiguration> {
  return client.get('/api/v1/admin/sso/configuration', options);
}

/** Lists connectors, newest first. */
export function listManagedConnectors(
  query: { limit: number; cursor?: string | undefined },
  options?: RequestOptions,
): Promise<ManagedConnectorPage> {
  const { limit, cursor } = query;
  return client.get('/api/v1/admin/sso/connectors', {
    ...options,
    query: { limit, ...(cursor ? { cursor } : {}) },
  });
}

/**
 * Adds a connector with the ID the caller chose, so its callback URLs could be registered first.
 * Reuse `idempotencyKey` when retrying the same request.
 */
export function createConnector(
  input: {
    id: string;
    name: string;
    origin: string;
    clientId: string;
    clientSecret: string;
    enabled: boolean;
    reason: string;
  },
  idempotencyKey: string,
): Promise<ManagedConnector> {
  return client.post('/api/v1/admin/sso/connectors', {
    body: {
      id: input.id,
      name: input.name,
      provider: 'gitea',
      origin: input.origin,
      client_id: input.clientId,
      client_secret: input.clientSecret,
      enabled: input.enabled,
      reason: input.reason,
    },
    idempotencyKey,
  });
}

/**
 * Renames, turns on or off, or replaces the credentials of a connector at the revision it was
 * read at. Turning it off or replacing its credentials ends the sessions that started through
 * it. Reuse `idempotencyKey` when retrying the same request.
 */
export function updateConnector(
  connector: Pick<ManagedConnector, 'id' | 'revision'>,
  change: {
    name: string;
    enabled: boolean;
    credentials: { clientId: string; clientSecret: string } | null;
    reason: string;
  },
  idempotencyKey: string,
): Promise<ManagedConnector> {
  return client.patch('/api/v1/admin/sso/connectors/{connector_id}', {
    path: { connector_id: connector.id },
    body: {
      revision: connector.revision,
      name: change.name,
      enabled: change.enabled,
      client_id: change.credentials?.clientId ?? null,
      client_secret: change.credentials?.clientSecret ?? null,
      reason: change.reason,
    },
    idempotencyKey,
  });
}

/** Checks a stored connector against its provider. A failed check is a result, not an error. */
export function testConnector(id: string): Promise<ConnectorCheck> {
  return client.post('/api/v1/admin/sso/connectors/{connector_id}/tests', {
    path: { connector_id: id },
  });
}
