# 0020: Sending a failed email again

Status: accepted.

## Context

UX-120 asks that administrators see a failed invitation or reset email and send it again.
The control plane already listed emails (`GET /api/v1/admin/mail-deliveries`) and could
reissue an invitation, but nothing showed the list and a failed reset email had no way
back. A finished email keeps no payload: the outbox erases the encrypted link when it
finishes, so the original email cannot be sent as it was.

## Decision

- **A resend issues anew what the email carried.** `POST
  /api/v1/admin/mail-deliveries/{id}/resends` reissues an invitation through the same code
  as the invitation resend (a new link, the old one stops working), replaces an unused reset
  token with a new one sent to the account's current address, or queues the
  password-changed notice again. It returns the new email.
- **Only the newest failed email of its subject.** A queued email still retries on its own,
  a sent one arrived, and an older failure was replaced by a newer email whose link is the
  live one. Anything else answers `MAIL_DELIVERY_NOT_RESENDABLE`. An invitation that was
  accepted or revoked answers `INVITATION_STATE_CONFLICT`; a disabled account gets nothing.
- **Idempotent like invitation writes.** The request needs an `Idempotency-Key`; the
  record shares `idempotency_records` (migration 000035 allows the new operation). The
  invitation code moved into `inviteWithin` and a shared `idempotentWithin`, so both
  writes keep one copy of that logic. Resends count against the administrator's invitation
  rate and are audited as `mail.resent`.
- **PostgreSQL only.** Other stores answer `MAIL_RESEND_UNSUPPORTED` (CMP-201, CMP-202).

## Consequences

- The console's Email delivery page lists emails with a status filter, says in words why
  one failed and what to check, and offers "Send again" on failed rows. A retried click
  reuses its key, so it cannot send twice.
- The page cannot change SMTP settings; those stay in the installation configuration.
