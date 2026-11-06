# Keyboard shortcuts

Research behind UX-122: one-character shortcuts for common actions, and the setting that turns them off.

## What the standard requires

WCAG 2.2 success criterion 2.1.4, [Character Key Shortcuts](https://www.w3.org/WAI/WCAG22/Understanding/character-key-shortcuts.html)
(level A): a shortcut made only of letter, punctuation, number or symbol characters must be possible to
turn off, or to remap to include a non-printable key, or be active only while its component has focus.
People using speech input are the main concern: a spoken word arrives as letters, each of which would
fire. So the console cannot ship `N` or `/` without the switch, and the switch must be reachable when
the shortcuts are off.

## How others solve it

- **GitHub** ([keyboard shortcuts](https://docs.github.com/en/get-started/accessibility/keyboard-shortcuts)):
  shortcuts are on, `?` lists those of the page, and "you can disable character key shortcuts, while
  still allowing shortcuts that use modifier keys" in the
  [accessibility settings](https://docs.github.com/en/account-and-profile/how-tos/account-settings/managing-accessibility-settings).
- **Gmail** ([keyboard shortcuts](https://support.google.com/mail/answer/6594)): off until "Keyboard
  shortcuts on" is chosen in the settings, and the question key opens the list.

Both keep the choice on the account, list the shortcuts under `?`, and leave modifier shortcuts alone.

## Choices

- **On by default, as GitHub does.** The design system counts them among the common paths, and the
  prototype shows them. The switch satisfies the criterion in either position.
- **One switch for every one-character key**, not per key: the criterion's concern is any letter
  firing. `Ctrl K` or `⌘ K` has a modifier and stays.
- **It follows the account**, in a new `single_key_shortcuts` preference, like theme and density, and
  this browser remembers it so it applies before the account has loaded. It sits on the account page and
  in the list itself, which the command menu also opens, so it is reachable with the keys off.
- **Keys never act while typing, with a modifier, during composition, on repeat, or behind a dialog
  or menu.** The prototype's rule, plus composition for Chinese input and repeat for a held key.
- **`?` lists, `/` goes to the page's search** as the interaction system and GitHub do; `/` does nothing
  on a page without one.
- **An action whose page does not exist says so**: listed as not available yet, and pressing it says the
  same. Hiding it would make the key silently dead, and ADR 0036 shows every option with a reason.

Only `N` has a page today. `D`, `I` and `K` need deploying, the catalog and API keys, so UX-122 stays open
until they have. `j` and `k` for moving through rows are in the interaction system and are UX-128.

Rejected: remapping keys, which is more work for a person than the off switch and which neither GitHub
nor Gmail offers in this form; shortcuts active only on focus, which removes the point of a global key.

## Licences

No new dependency.

## Failure modes and tests

- Typing a letter in a field, with a modifier, or with a dialog open: nothing happens; browser flow.
- `N` on the Nodes page itself, where the hash arrives after the page mounted; browser flow.
- Off: no one-character key acts and the command menu still opens the list; browser flow.
- Another browser's choice arrives with the account; browser flow and the live run.
- Two browsers changing different fields: an update names only its field; Go test on PostgreSQL.
- Upgrading keeps shortcuts on without a new revision; Go test on PostgreSQL and the live upgrade.
- MySQL and Dameng chains lack the column: they read as on and refuse off; Go test.

## What done means

With shortcuts on, each of D, I, N and K runs its action from anywhere, `/` and `?` work, and turning
them off in the list or on the account page stops all of them for that account in every browser.
