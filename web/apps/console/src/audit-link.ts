// The address of the audit trail narrowed to one event, which keeps working as time passes.
import type { AuditEvent } from '@ownsolid/api';

/** The value of the `range` filter that reads `from` and `to`. */
export const customRange = 'custom';

// How far around an event its link reaches, so the event is inside the window it asks for.
const eventLinkMilliseconds = 60_000;

/** The query string of a list narrowed to one event, with its drawer open. */
export function auditEventSearch(event: AuditEvent) {
  const moment = Date.parse(event.occurred_at);
  return {
    range: customRange,
    from: new Date(moment - eventLinkMilliseconds).toISOString(),
    to: new Date(moment + eventLinkMilliseconds).toISOString(),
    open: event.id,
  };
}
