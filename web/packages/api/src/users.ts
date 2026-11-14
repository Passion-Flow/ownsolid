// Accounts and invitations, for platform administrators (api/openapi/v1/identity).
import { createClient } from './client.ts';
import type { identityContract } from './gen/index.ts';
import type { User } from './identity.ts';
import type { RequestOptions } from './transport.ts';

type Schemas = identityContract.components['schemas'];

/** Whether an account can sign in. */
export type UserStatus = User['status'];

/** One page of accounts. */
export type UserPage = Schemas['UserListResponse']['data'];

/** An invitation and its latest email. */
export type Invitation = Schemas['Invitation'];

/** Where an invitation stands. */
export type InvitationStatus = Invitation['status'];

const client = createClient<identityContract.paths>();

/** Lists accounts, newest first. */
export function listUsers(
  query: {
    limit: number;
    cursor?: string | undefined;
    status?: UserStatus | undefined;
    emailPrefix?: string | undefined;
    /** Text anywhere in the address or display name, in any case. */
    search?: string | undefined;
  },
  options?: RequestOptions,
): Promise<UserPage> {
  const { limit, cursor, status, emailPrefix, search } = query;
  return client.get('/api/v1/admin/users', {
    ...options,
    query: {
      limit,
      ...(cursor ? { cursor } : {}),
      ...(status ? { status } : {}),
      ...(emailPrefix ? { email_prefix: emailPrefix } : {}),
      ...(search ? { q: search } : {}),
    },
  });
}

/** Reads one account. */
export function getUser(id: string, options?: RequestOptions): Promise<User> {
  return client.get('/api/v1/admin/users/{user_id}', { ...options, path: { user_id: id } });
}

/** Disables or enables an account. Disabling ends its sessions at once. */
export function setUserStatus(
  id: string,
  change: { status: UserStatus; version: number; reason: string },
  options?: RequestOptions,
): Promise<User> {
  return client.patch('/api/v1/admin/users/{user_id}/status', {
    ...options,
    path: { user_id: id },
    body: change,
  });
}

/** Lists invitations in one state, newest first. */
export function listInvitations(
  query: { limit: number; status: InvitationStatus },
  options?: RequestOptions,
): Promise<Schemas['InvitationListResponse']['data']> {
  return client.get('/api/v1/admin/invitations', { ...options, query });
}

/** Invites someone. Reuse `idempotencyKey` when retrying, so a retry cannot invite twice. */
export function createInvitation(
  input: { email: string; locale: string },
  idempotencyKey: string,
  options?: RequestOptions,
): Promise<Invitation> {
  return client.post('/api/v1/admin/invitations', { ...options, body: input, idempotencyKey });
}

/** Sends an invitation again with a new link. Reuse `idempotencyKey` across retries. */
export function resendInvitation(
  id: string,
  version: number,
  idempotencyKey: string,
  options?: RequestOptions,
): Promise<Invitation> {
  return client.post('/api/v1/admin/invitations/{invitation_id}/resends', {
    ...options,
    path: { invitation_id: id },
    body: { version },
    idempotencyKey,
  });
}
