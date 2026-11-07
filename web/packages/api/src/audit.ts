// The audit trail, for platform administrators (api/openapi/v1/identity).
import { createClient } from './client.ts';
import type { identityContract } from './gen/index.ts';
import type { RequestOptions } from './transport.ts';

type Schemas = identityContract.components['schemas'];

/** One recorded action. */
export type AuditEvent = Schemas['AuditEvent'];

/** How an action ended. */
export type AuditOutcome = AuditEvent['outcome'];

/** One page of events. */
export type AuditPage = Schemas['AuditListResponse']['data'];

const client = createClient<identityContract.paths>();

/**
 * Lists events, newest first. The first page sets the window, which its cursor then carries, so
 * later pages pass only the cursor and the filters and the list cannot shift while it is read.
 */
export function listAuditEvents(
  query: {
    limit: number;
    cursor?: string | undefined;
    category?: string | undefined;
    outcome?: AuditOutcome | undefined;
    actorUserId?: string | undefined;
    requestId?: string | undefined;
    /** Only events about this resource. */
    targetId?: string | undefined;
    from?: string | undefined;
    to?: string | undefined;
  },
  options?: RequestOptions,
): Promise<AuditPage> {
  const { limit, cursor, category, outcome, actorUserId, requestId, targetId, from, to } = query;
  return client.get('/api/v1/admin/audit-events', {
    ...options,
    query: {
      limit,
      ...(cursor ? { cursor } : { ...(from ? { from } : {}), ...(to ? { to } : {}) }),
      ...(category ? { category } : {}),
      ...(outcome ? { outcome } : {}),
      ...(actorUserId ? { actor_user_id: actorUserId } : {}),
      ...(requestId ? { request_id: requestId } : {}),
      ...(targetId ? { target_id: targetId } : {}),
    },
  });
}
