// Same-origin JSON transport for the control-plane API: envelopes, problems,
// timeouts and CSRF tokens for writes.
import { ApiProblem, type FieldProblem } from './problem.ts';

/** Options for a single API call. */
export interface RequestOptions {
  /** Cancels the call, for example when the page that started it closes. */
  signal?: AbortSignal | undefined;
  /**
   * Identifies one logical write so the server applies it once even when it is
   * retried. Generate it when the user starts the action and reuse it for retries.
   */
  idempotencyKey?: string | undefined;
  /**
   * Marks a read the person did not ask for, such as a poll, so it does not count as
   * activity: an open tab must not hold a session past its idle timeout.
   */
  background?: boolean | undefined;
}

/** HTTP methods that change state; they carry a CSRF token. */
export type WriteMethod = 'POST' | 'PUT' | 'PATCH' | 'DELETE';

const defaultTimeoutMilliseconds = 30_000;
let timeoutMilliseconds = defaultTimeoutMilliseconds;

/** Sets the per-request timeout, normally from the runtime configuration. */
export function setRequestTimeout(milliseconds: number): void {
  timeoutMilliseconds = milliseconds;
}

interface CsrfToken {
  token: string;
  expiresAt: number;
}

// Tokens are bound to the session, so they are dropped whenever it may change.
let csrf: Promise<CsrfToken> | null = null;
// Renew a little before expiry so a token does not lapse in flight.
const csrfMarginMilliseconds = 30_000;

/** Forgets the cached CSRF token. Call after sign-in and sign-out. */
export function resetCsrf(): void {
  csrf = null;
}

function retryAfter(header: string | null): number | undefined {
  if (!header) return undefined;
  const seconds = Number(header);
  return Number.isFinite(seconds) && seconds >= 0 ? seconds : undefined;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function fieldProblems(value: unknown): FieldProblem[] {
  if (!Array.isArray(value)) return [];
  return value.filter(
    (item): item is FieldProblem =>
      isRecord(item) && typeof item.field === 'string' && typeof item.code === 'string',
  );
}

async function send(path: string, init: RequestInit, signal?: AbortSignal): Promise<Response> {
  const timeout = AbortSignal.timeout(timeoutMilliseconds);
  try {
    return await fetch(path, {
      ...init,
      credentials: 'same-origin',
      cache: 'no-store',
      signal: signal ? AbortSignal.any([signal, timeout]) : timeout,
    });
  } catch (error) {
    if (signal?.aborted) throw error;
    if (timeout.aborted) throw new ApiProblem('REQUEST_TIMEOUT');
    throw new ApiProblem('NETWORK_UNAVAILABLE');
  }
}

async function read<T>(response: Response): Promise<T> {
  const requestId = response.headers.get('X-Request-ID') ?? '';
  if (response.status === 204) return undefined as T;
  let body: unknown;
  try {
    body = await response.json();
  } catch {
    throw new ApiProblem('RESPONSE_INVALID', { status: response.status, requestId });
  }
  if (!response.ok) {
    if (!isRecord(body) || typeof body.code !== 'string' || body.code === '') {
      throw new ApiProblem('RESPONSE_INVALID', { status: response.status, requestId });
    }
    throw new ApiProblem(body.code, {
      status: response.status,
      requestId: typeof body.request_id === 'string' ? body.request_id : requestId,
      retryAfterSeconds: retryAfter(response.headers.get('Retry-After')),
      fields: fieldProblems(body.fields),
    });
  }
  if (!isRecord(body) || !('data' in body)) {
    throw new ApiProblem('RESPONSE_INVALID', { status: response.status, requestId });
  }
  return body.data as T;
}

/** Reads a resource. `path` is absolute, such as `/api/v1/me`. */
export async function get<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const init: RequestInit = options.background
    ? { method: 'GET', headers: { 'X-Ownsolid-Background': 'true' } }
    : { method: 'GET' };
  return read<T>(await send(path, init, options.signal));
}

/** A file the server sent, and the name it gave it. */
export interface DownloadedFile {
  content: Blob;
  name: string;
}

/** Reads a download. A failure is the same `ApiProblem` any other read raises. */
export async function getFile(path: string, options: RequestOptions = {}): Promise<DownloadedFile> {
  const response = await send(path, { method: 'GET' }, options.signal);
  if (!response.ok) return read<never>(response);
  const disposition = response.headers.get('Content-Disposition') ?? '';
  const name = /filename="?([^";]+)"?/.exec(disposition)?.[1] ?? 'download';
  return { content: await response.blob(), name };
}

function csrfToken(signal?: AbortSignal): Promise<CsrfToken> {
  if (csrf) {
    const cached = csrf;
    return cached.then((token) => {
      if (token.expiresAt - csrfMarginMilliseconds > Date.now()) return token;
      if (csrf === cached) csrf = null;
      return csrfToken(signal);
    });
  }
  const pending = get<{ token: string; expires_at: string }>('/api/v1/auth/csrf', {
    ...(signal ? { signal } : {}),
  }).then((data) => {
    if (typeof data.token !== 'string' || data.token === '')
      throw new ApiProblem('RESPONSE_INVALID');
    const expiresAt = Date.parse(data.expires_at);
    return { token: data.token, expiresAt: Number.isNaN(expiresAt) ? 0 : expiresAt };
  });
  csrf = pending;
  pending.catch(() => {
    if (csrf === pending) csrf = null;
  });
  return pending;
}

/** Sends a write with a CSRF token. A refused token is replaced once and the call repeated. */
async function withCsrf<T>(
  path: string,
  init: (token: string) => RequestInit,
  signal: AbortSignal | undefined,
): Promise<T> {
  const attempt = async (): Promise<T> => {
    const { token } = await csrfToken(signal);
    return read<T>(await send(path, init(token), signal));
  };
  try {
    return await attempt();
  } catch (error) {
    if (!(error instanceof ApiProblem) || error.code !== 'CSRF_INVALID') throw error;
    resetCsrf();
    return attempt();
  }
}

/**
 * Changes state. The call carries a CSRF token, fetched and cached as needed. When
 * the server rejects the token, the call is repeated once with a fresh token.
 */
export function write<T>(
  method: WriteMethod,
  path: string,
  body?: unknown,
  options: RequestOptions = {},
): Promise<T> {
  return withCsrf<T>(
    path,
    (token) => {
      const headers: Record<string, string> = { 'X-CSRF-Token': token };
      if (body !== undefined) headers['Content-Type'] = 'application/json';
      if (options.idempotencyKey) headers['Idempotency-Key'] = options.idempotencyKey;
      const init: RequestInit = { method, headers };
      if (body !== undefined) init.body = JSON.stringify(body);
      return init;
    },
    options.signal,
  );
}

/** Sends bytes as they are, with the headers the operation names. */
export function writeBytes<T>(
  path: string,
  body: Blob,
  headers: Readonly<Record<string, string>>,
  options: RequestOptions = {},
): Promise<T> {
  return withCsrf<T>(
    path,
    (token) => ({
      method: 'POST',
      headers: { ...headers, 'Content-Type': 'application/octet-stream', 'X-CSRF-Token': token },
      body,
    }),
    options.signal,
  );
}
