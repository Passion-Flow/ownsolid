// Email delivery status and resends, for platform administrators (api/openapi/v1/identity).
import { createClient } from './client.ts';
import type { identityContract } from './gen/index.ts';
import type { RequestOptions } from './transport.ts';

type Schemas = identityContract.components['schemas'];

/** One email and how its delivery went. */
export type MailDelivery = Schemas['MailDelivery'];

/** Where an email is in its delivery. */
export type MailState = MailDelivery['state'];

/** Why an email was sent. */
export type MailPurpose = MailDelivery['purpose'];

/** One page of emails, newest first. */
export type MailDeliveryPage = Schemas['MailListResponse']['data'];

const client = createClient<identityContract.paths>();

/** Lists emails, newest first, optionally only those in one state. */
export function listMailDeliveries(
  query: {
    limit: number;
    cursor?: string | undefined;
    state?: MailState | undefined;
    purpose?: MailPurpose | undefined;
    recipientPrefix?: string | undefined;
    /** Newest first unless `oldest`. */
    order?: 'oldest' | undefined;
  },
  options?: RequestOptions,
): Promise<MailDeliveryPage> {
  const { limit, cursor, state, purpose, recipientPrefix, order } = query;
  return client.get('/api/v1/admin/mail-deliveries', {
    ...options,
    query: {
      limit,
      ...(cursor ? { cursor } : {}),
      ...(state ? { state } : {}),
      ...(purpose ? { purpose } : {}),
      ...(recipientPrefix ? { recipient_prefix: recipientPrefix } : {}),
      ...(order ? { order } : {}),
    },
  });
}

/**
 * Sends a failed email again and returns the new email. Reuse `idempotencyKey` when
 * retrying the same request, so it cannot send twice.
 */
export function resendMailDelivery(id: string, idempotencyKey: string): Promise<MailDelivery> {
  return client.post('/api/v1/admin/mail-deliveries/{mail_delivery_id}/resends', {
    path: { mail_delivery_id: id },
    body: {},
    idempotencyKey,
  });
}
