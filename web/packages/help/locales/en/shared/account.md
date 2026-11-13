# Your account

[Your account](/account) holds your profile, your password and how ownsolid looks for you.

## What you can change

- **Profile**: your display name.
- **Password**: changing it signs out your other sessions.
- **Language**: the language of every page. The user menu at the top right changes it too.
- **Sign-in providers**: link or unlink an account on a single sign-on provider. Unlinking asks for
  your password.

The theme and density buttons at the top of every page switch between light and dark and between
comfortable and compact rows. Like the language, they follow your account to every browser.

Before a session ends for inactivity, a warning offers to keep you signed in.

## Check your sign-ins

Sessions lists your active sign-ins at this entrance. Admin and Portal have separate lists.
Each entry shows its sign-in method, sign-in time, last activity and expiry as returned by
the server. It does not identify a device or location. **Current session** identifies the
sign-in making this request; use **Sign out** in the account menu to leave it.

Use **Refresh** to check the first page and **Load more sessions** to continue. The list
does not refresh in the background. A partial failure keeps earlier pages; an expired
cursor offers to restart from the first page. A permission refusal hides earlier entries.

To end another sign-in, choose **End session** and type its session ID. This immediately
ends its access and cannot be undone. A confirmed end reads the list again. If the result
is uncertain, choose **Read again** before another explicit attempt. A missing entry or
not-found reply alone does not prove the end succeeded. The page never automatically
replays an end request. When a retry delay is shown, wait until it ends before retrying.

Changing your password replaces this browser's session and ends all your other sessions.
The Sessions section starts a new read rather than reusing the previous current marker.
