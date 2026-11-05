# 0025: Language preference

Status: accepted.

## Context

UX-109 asks for a language per person, a tenant default and browser negotiation. Each browser
remembered its own choice and otherwise followed its languages. `users.locale` exists for emails
but cannot be the preference: it is never empty, an invitation sets it, and using it would flip
a person with a Chinese browser to the English their account was created with.

## Decision

- **The order is the person's choice, then the tenant's default, then the browser.** A choice
  follows the account across browsers, like the theme.
- **`user_preferences.locale` holds the choice, and `auto` means none,** the same way `system`
  works for the theme. Existing accounts start at `auto`, so nobody's language changes.
- **A choice also sets `users.locale`, which emails read.** Going back to automatic sets it to
  the tenant's default. With no default the account keeps what it has, because a browser's
  languages cannot reach an email.
- **`tenants.default_locale` holds the default, and the public runtime configuration serves
  it,** so the sign-in page can use it. A language is not sensitive. If the read fails, the
  answer is `auto`: the page follows the browser rather than failing to open.
- **Administrators set it on a new Settings page,** on top of a revision, audited as
  `tenant.default_locale_changed`.
- **A choice made before signing in reaches the account.** Someone who picks a language on the
  sign-in page must not lose it when an `auto` account loads. A marker says the stored language
  matches the account; without it, the stored language is a choice to save. A language stored
  before this change has no marker, so it is saved too, which keeps people on what they chose.
- **Only PostgreSQL stores it.** MySQL and Dameng read as `auto` and refuse a choice, like the
  density (CMP-201, CMP-202).

## Consequences

- A language chosen on a shared computer's sign-in page is saved to whoever signs in next, and
  they can change it on the Account page. Keeping it only in the browser would leave their
  other browsers and their emails in another language.
- Choosing a language moves the account's version, so a profile form open in another browser
  gets a version conflict and reloads the current values.
