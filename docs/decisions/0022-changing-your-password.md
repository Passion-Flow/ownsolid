# 0022: Changing your own password

Status: accepted.

## Context

UX-119 asks for an account page with a password change. Only the emailed reset could set a
password, and it ends every session, the caller's included. Someone who knows their
password should not have to wait for an email, or sign in again, to change it.

## Decision

- **`POST /api/v1/me/password-changes` takes the current and the new password.** The current
  one must verify, and the new one must pass the password policy and differ from it.
  Refusals are `PASSWORD_CURRENT_INVALID` and `PASSWORD_UNCHANGED`, which name the field to
  fix, unlike sign-in's deliberately vague answer: the caller already holds a session.
- **Only the calling browser stays signed in.** A change ends every session of the account,
  stops unused reset links and cancels their emails, because a person changes a password
  when they suspect someone else has it. The transaction then issues this browser a new
  session, and the response replaces its cookie. CSRF tokens are bound to the session, so
  the client fetches a new one.
- **The account is told by email**, using the password-changed notice a reset already
  sends, and the change is audited as `auth.password_changed`. A wrong current password is
  audited as `auth.password_change_failed`, so guessing from a stolen session leaves a trace.
- **Attempts share the sign-in limits, per account and per address,** in buckets of their
  own. Each attempt tests a password, so a session must not be a cheaper way to guess than
  the sign-in page.
- **No `Idempotency-Key`.** The first response carries a session token the server keeps
  only as a hash, so a replay could not return it. A retry after a lost response finds the
  old session ended and the person signs in with the new password, which is the correct end
  state. Sign-in and reset take no key for the same reason.
- **It works without a license,** like reset and profile changes: a security action must not
  stop when a license lapses.

The account page shows the sign-in providers through the existing binding API. Linking and
unlinking keep that API's password and reason requirements. The page fills in the reason, so
the person only confirms a password, and they can change the text recorded in the audit log.

## Consequences

- A request already in flight with the old cookie can come back as signed out just after a
  change, and that response clears the new cookie. The person signs in again with the new
  password. The window is one request long, and closing it would need the old session to
  outlive the change, which is what the change must prevent.
- A session started through single sign-on can change the password too. The new session
  it receives is a password session with no provider source.
