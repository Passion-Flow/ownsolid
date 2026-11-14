import { isProblem, listJoinTokens, type JoinToken } from '@ownsolid/api';
import type { EnrollmentCsvRow } from './node-enrollment-csv.ts';

type TokenReceipt = Pick<JoinToken, 'id' | 'created_at' | 'expires_at' | 'node_id'>;
type Refusal = 'rate' | 'capacity' | 'rollback' | 'denied' | 'workspace' | 'invalid' | 'session';

export type EnrollmentResult =
  | { state: 'issuing' }
  | { state: 'unknown'; requestId: string }
  | { state: 'failed'; reason: Refusal; requestId: string; retryAt?: number }
  | { state: JoinToken['status']; token: TokenReceipt };

export interface EnrollmentDraft {
  workspace: string;
  file: string;
  rows: EnrollmentCsvRow[];
  results: Partial<Record<number, EnrollmentResult>>;
}

/** Control retains the only durable copy of token state. Browser drafts contain no secrets. */
export function readEnrollmentDraft(key: string): EnrollmentDraft | null {
  try {
    const raw = sessionStorage.getItem(key);
    if (!raw) return null;
    const draft = JSON.parse(raw) as EnrollmentDraft;
    // Reload could have interrupted a reply after Control committed a token.
    for (const row of draft.rows) {
      if (draft.results[row.line]?.state === 'issuing') {
        draft.results[row.line] = { state: 'unknown', requestId: '' };
      }
    }
    return draft;
  } catch {
    return null;
  }
}

/** Stores the preview and public receipts for this operator and browser tab. */
export function saveEnrollmentDraft(key: string, draft: EnrollmentDraft | null): void {
  try {
    if (draft) sessionStorage.setItem(key, JSON.stringify(draft));
    else sessionStorage.removeItem(key);
  } catch {
    // The page keeps the draft in memory when tab storage is unavailable.
  }
}

/** Copies the public receipt explicitly so a minted secret cannot enter draft storage. */
export function enrollmentReceipt(token: JoinToken): EnrollmentResult {
  const { id, created_at, expires_at, node_id } = token;
  return { state: token.status, token: { id, created_at, expires_at, node_id } };
}

/** Unknown write outcomes need reconciliation before a replacement is safe. */
export function enrollmentFailure(error: unknown): EnrollmentResult {
  const requestId = isProblem(error) ? error.requestId : '';
  if (isProblem(error, 'RATE_LIMITED')) {
    return {
      state: 'failed',
      reason: 'rate',
      requestId,
      retryAt: Date.now() + (error.retryAfterSeconds ?? 5) * 1000,
    };
  }
  const known = [
    ['RESOURCE_LIMIT_REACHED', 'capacity'],
    ['STORAGE_COMMIT_ROLLED_BACK', 'rollback'],
    ['ACCESS_DENIED', 'denied'],
    ['RESOURCE_NOT_FOUND', 'workspace'],
    ['VALIDATION_FAILED', 'invalid'],
    ['AUTH_REQUIRED', 'session'],
    ['AUTH_SESSION_EXPIRED', 'session'],
  ] as const;
  const match = known.find(([code]) => isProblem(error, code));
  return match ? { state: 'failed', reason: match[1], requestId } : { state: 'unknown', requestId };
}

/** Only confirmed transient refusals can be retried in place. */
export function retryEnrollment(result: EnrollmentResult | undefined): boolean {
  return (
    result?.state === 'failed' &&
    (result.reason === 'rate' || result.reason === 'capacity' || result.reason === 'rollback')
  );
}

const historyPageSize = 100;
const maximumHistoryPages = 10;

/** Reads actual joins by token ID and workspace, without using a node-name search. */
export async function findEnrollmentReceipts(
  workspace: string,
  targets: readonly TokenReceipt[],
  signal: AbortSignal,
): Promise<JoinToken[]> {
  const remaining = new Set(targets.map(({ id }) => id));
  const earliest = targets.reduce(
    (time, target) => Math.min(time, Date.parse(target.created_at)),
    Infinity,
  );
  const found: JoinToken[] = [];
  let cursor: string | undefined;
  for (let page = 0; page < maximumHistoryPages && remaining.size > 0; page++) {
    const response = await listJoinTokens(
      { limit: historyPageSize, cursor, includeHistory: true },
      { signal, background: true },
    );
    for (const token of response.items) {
      if (token.workspace_id !== workspace || (token.status === 'consumed' && !token.node_id))
        continue;
      if (remaining.delete(token.id)) found.push(token);
    }
    const last = response.items.at(-1);
    if (!response.next_cursor || !last || Date.parse(last.created_at) < earliest) break;
    cursor = response.next_cursor;
  }
  return found;
}
