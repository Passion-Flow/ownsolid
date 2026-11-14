// The signed-in person's own account: profile, password and linked sign-in providers
// (api/openapi/v1/identity and api/openapi/v1/sso).
import { createClient } from './client.ts';
import type { identityContract, ssoContract } from './gen/index.ts';
import type { Me } from './identity.ts';
import { resetCsrf, type RequestOptions } from './transport.ts';

/** The profile fields to save, on top of the account version they were read at. */
export type ProfileChange = identityContract.components['schemas']['UpdateMeRequest'];

/** A sign-in provider account linked to this account. */
export type SsoBinding = ssoContract.components['schemas']['SSOBinding'];

const identity = createClient<identityContract.paths>();
const sso = createClient<ssoContract.paths>();

/** Saves the profile. Fails with `VERSION_CONFLICT` when the account changed meanwhile. */
export function updateProfile(change: ProfileChange): Promise<Me> {
  return identity.patch('/api/v1/me', { body: change });
}

/**
 * Changes the password. Every other session ends and this browser gets a new one, so
 * the identity it returns replaces the cached session.
 */
export async function changePassword(currentPassword: string, newPassword: string): Promise<Me> {
  try {
    return await identity.post('/api/v1/me/password-changes', {
      body: { current_password: currentPassword, new_password: newPassword },
    });
  } finally {
    // The session changed, and CSRF tokens are bound to it.
    resetCsrf();
  }
}

/** Lists the provider accounts linked to this account, past links included. */
export async function listSsoBindings(options?: RequestOptions): Promise<SsoBinding[]> {
  return (await sso.get('/api/v1/account/sso/bindings', options)).items;
}

/** Starts linking a provider and returns the provider's address to go to. */
export async function startSsoLink(
  connectorId: string,
  password: string,
  reason: string,
): Promise<string> {
  const started = await sso.post('/api/v1/auth/sso/{connector_id}/starts', {
    path: { connector_id: connectorId },
    body: { mode: 'link', password, reason },
  });
  return started.authorization_url;
}

/** Unlinks a provider account. Reuse `idempotencyKey` when retrying the same request. */
export function unlinkSsoBinding(
  binding: Pick<SsoBinding, 'id' | 'revision'>,
  password: string,
  reason: string,
  idempotencyKey: string,
): Promise<SsoBinding> {
  return sso.post('/api/v1/account/sso/bindings/{binding_id}/unlink', {
    path: { binding_id: binding.id },
    body: { revision: binding.revision, password, reason },
    idempotencyKey,
  });
}
