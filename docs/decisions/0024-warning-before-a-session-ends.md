# 0024: Warning before a session ends

Status: accepted.

## Context

UX-113 asks for a warning before a session ends from inactivity, with one action to stay
signed in. The session could not end that way while a page was open. Every authenticated
request moved `last_seen_at`, and the notification bell polls every five seconds, so an open
tab held the session until its absolute limit. The idle timeout protected only browsers that
had been closed, which is the opposite of what it is for. A warning alone would never fire.

The browser also could not know when to warn: `GET /api/v1/me` says nothing about the
session, and the sign-in response is gone after a reload.

## Decision

- **A read the person did not ask for does not count as activity.** A `GET` carrying
  `X-Ownsolid-Background: true` authenticates as usual and leaves `last_seen_at` alone. Writes
  always count. The header needs no protection: leaving it out only keeps the client's own
  session alive, which any request of its own could do. New polls must send it, through the
  API client's `background` option; a poll that forgets it brings the old flaw back.
- **`GET /api/v1/me/session` says what is left, in seconds.** Durations rather than times, so a
  browser with a wrong clock still counts down correctly. It also returns how long before the
  end to warn: `identity.session_warning_seconds`, shortened to half the idle timeout and to
  what leaves one touch interval first. A request made during the warning must be able to
  move `last_seen_at`, which is only touched once per interval.
- **Working in a page counts as activity even when it makes no requests.** Someone filling in
  a long form must not be signed out. While there has been input since the last check, the
  page sends an ordinary read once per warning window, so a session outlives the last input
  by at most the idle timeout plus one window.
- **Once the warning shows, only a choice extends the session.** A stray mouse movement must
  not. Enter, the focused button, or Escape all keep it: pressing a key shows someone is here.
- **The server decides, not the tab.** While the warning shows the page keeps reading in the
  background, so activity in another tab closes it, and a tab whose server cannot be reached
  keeps warning instead of signing anyone out.
- **The time limit gets its own wording.** When the absolute limit comes first, staying
  cannot help, so the dialog says so and offers to sign in again or keep working. It does not
  return once dismissed.
- **The sign-in page says the session ended,** when a request found it over while a page was
  open, and returns to that page afterwards. A visitor who was already signed out, or who
  signed out themselves, sees no such notice.

## Consequences

- An unattended open page now signs out at the idle timeout. Before this, a console left open
  stayed signed in for up to the absolute limit. A wall display (UX-201) needs its own answer.
- An active tab makes one more request per warning window, two minutes by default.
- The warning relies on timers that browsers slow in hidden tabs. The page looks again when it
  becomes visible, and the server enforces the timeout either way.
