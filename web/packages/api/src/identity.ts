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

/** How long the session has left, in seconds, so a wrong browser clock does not matter. */
export type SessionStatus = Schemas['SessionStatus'];

/**
 * Reads what is left of the session. Pass `background` for a check nobody asked for;
 * without it the read counts as activity and extends the session.
 */
export function getSessionStatus(options?: RequestOptions): Promise<SessionStatus> {
  return client.get('/api/v1/me/session', options);
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

/** The address and language an invitation was issued for. */
export type InvitationDetails = Success<identityContract.operations['validateInvitation']>;

/** What {@link register} sends. */
export type Registration = Schemas['RegisterRequest'];

/** Checks an invitation link. Fails with `TOKEN_UNUSABLE` when it expired or was used. */
export function validateInvitation(
  token: string,
  options?: RequestOptions,
): Promise<InvitationDetails> {
  return client.post('/api/v1/auth/invitation-validations', { ...options, body: { token } });
}

/** Creates the invited account. It then signs in like any other. */
export async function register(registration: Registration): Promise<void> {
  await client.post('/api/v1/auth/register', { body: registration });
}

/**
 * Asks for a password reset email. The server answers the same whether or not the
 * address has an account, so this call cannot be used to find accounts.
 */
export async function requestPasswordReset(email: string): Promise<void> {
  await client.post('/api/v1/auth/password-reset-requests', { body: { email } });
}

/** Sets a new password with the token from a reset email. */
export async function resetPassword(token: string, password: string): Promise<void> {
  await client.post('/api/v1/auth/password-resets', { body: { token, password } });
}
