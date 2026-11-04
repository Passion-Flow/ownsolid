// Sign-in, sign-out and the current identity (api/openapi/v1/identity).
import { createClient, type Success } from './client.ts';
import type { identityContract } from './gen/index.ts';
import { resetCsrf, type RequestOptions } from './transport.ts';

type Schemas = identityContract.components['schemas'];

/** A user account as the identity API returns it. */
export type User = Schemas['User'];

/** The workspace a session works in by default. */
export type Workspace = Schemas['Workspace'];

/** The signed-in identity, from `GET /api/v1/me`. */
export type Me = Schemas['MeData'];

/** Which entrance a session belongs to: the console (admin) or the portal. */
export type Audience = Me['audience'];

/** Credentials for {@link signIn}. Passwords are sent as typed: never trimmed. */
export type Credentials = Schemas['LoginRequest'];

/** What a successful sign-in returns. */
export type SignInResult = Success<identityContract.operations['login']>;

const client = createClient<identityContract.paths>();

/** Reads the signed-in identity. Fails with `AUTH_REQUIRED` when there is no session. */
export function getMe(options?: RequestOptions): Promise<Me> {
  return client.get('/api/v1/me', options);
}

/** Signs in on the current origin. The session cookie is set by the server. */
export async function signIn(
  credentials: Credentials,
  options?: RequestOptions,
): Promise<SignInResult> {
  try {
    return await client.post('/api/v1/auth/login', { ...options, body: credentials });
  } finally {
    // The session changed, and CSRF tokens are bound to it.
    resetCsrf();
  }
}

/** Ends the current session on this origin. */
export async function signOut(options?: RequestOptions): Promise<void> {
  try {
    await client.post('/api/v1/auth/logout', options);
  } finally {
    resetCsrf();
  }
}
