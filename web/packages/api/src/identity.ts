// Sign-in, sign-out and the current identity (api/openapi/v1/identity).
import { get, resetCsrf, write, type RequestOptions } from './transport.ts';

/** Which entrance a session belongs to: the console (admin) or the portal. */
export type Audience = 'admin' | 'portal';

/** A user account as the identity API returns it. */
export interface User {
  id: string;
  email: string;
  display_name: string;
  locale: string;
  status: 'active' | 'disabled';
  is_platform_admin: boolean;
  email_verified_at: string | null;
  version: number;
  created_at: string;
  updated_at: string;
}

/** The workspace a session works in by default. */
export interface Workspace {
  id: string;
  tenant_id: string;
  name: string;
  role: 'owner' | 'member' | 'viewer';
}

/** The signed-in identity, from `GET /api/v1/me`. */
export interface Me {
  user: User;
  workspace: Workspace | null;
  audience: Audience;
}

/** What a successful sign-in returns. */
export interface SignInResult extends Me {
  session: { absolute_expires_at: string; idle_timeout_seconds: number };
}

/** Credentials for {@link signIn}. Passwords are sent as typed: never trimmed. */
export interface Credentials {
  email: string;
  password: string;
}

/** Reads the signed-in identity. Fails with `AUTH_REQUIRED` when there is no session. */
export function getMe(options?: RequestOptions): Promise<Me> {
  return get<Me>('/api/v1/me', options);
}

/** Signs in on the current origin. The session cookie is set by the server. */
export async function signIn(
  credentials: Credentials,
  options?: RequestOptions,
): Promise<SignInResult> {
  try {
    return await write<SignInResult>('POST', '/api/v1/auth/login', credentials, options);
  } finally {
    // The session changed, and CSRF tokens are bound to it.
    resetCsrf();
  }
}

/** Ends the current session on this origin. */
export async function signOut(options?: RequestOptions): Promise<void> {
  try {
    await write<undefined>('POST', '/api/v1/auth/logout', undefined, options);
  } finally {
    resetCsrf();
  }
}
