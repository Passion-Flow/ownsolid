# Design system

How ownsolid looks and behaves. The stack and visual decisions are recorded in
[ADR 0002](../decisions/0002-frontend-stack.md); this guide is the working standard
for every page in `web/apps/console` and `web/apps/portal`.

The owner approved the [UI prototype](design-prototype/index.html) on 2026-11-04; open
it in a browser (it runs offline) and match its look and layout. Ease of use is a core
product value. The owner rejected a Claude-like palette, serif headings and playful or
cartoon icons; keep the warm neutral, teal and Figtree direction below.

The tokens, fonts and components live in `web/packages/ui`. Reach for its components
before writing new markup: `Button`, `TextField`, `Card`, `StatusPill`, `Tag`, `Kbd`,
`Meter`, `Segmented`, `Switch`, `Checkbox`, `CopyText`, `EmptyState`, `Skeleton`, `Table`,
`Menu`, `Dialog`, `Drawer`, `Notice`, `ProblemNotice` and `useToast`.

## Tokens

| Token | Light | Dark |
| --- | --- | --- |
| Background | `#f6f4ef` | `#161513` |
| Surface | `#fffefb` | `#211f1c` |
| Surface, secondary | `#f1eee7` | `#2a2824` |
| Border | `#e5e0d5` | `#36332e` |
| Text | `#1d1b18` | `#f3f0ea` |
| Text, secondary | `#59544b` | `#bdb7ac` |
| Text, tertiary | `#706a60` | `#969084` |
| Accent (teal) | `#0d9488` | `#14a394` |
| Accent, strong | `#0b8378` | `#0b8378` |
| Status: good, warning, critical | `#0ca30c`, `#fab219`, `#d03b3b` | same |
| Critical text | `#c13737` | `#dd7070` |

- Spacing follows a 4-pixel scale. Radii: 8 px for controls, 12 to 14 px for cards,
  18 px for dialogs.
- The language is the person's choice, then the tenant's default on the Settings page, then the
  browser's ([ADR 0025](../decisions/0025-language-preference.md)). A choice saves to the account
  at once, and works before sign-in.
- Dates, numbers and units go through `format` from `useI18n()`, which follows the interface
  language; times show in the browser's time zone. Sizes use decimal units, as in the
  prototype. A lint rule rejects direct `Intl` and `toLocaleString` calls in pages.
- Typography: Figtree, 14 px base, page titles 27 px at weight 750; JetBrains Mono
  for identifiers, code and logs. Numbers in tables use tabular figures.
- Icons: Lucide at 18 px with a 1.75 stroke, set once in the stylesheet; import them
  from `lucide-react` and mark decorative ones `aria-hidden`.
- Theme and density belong to the account, chosen with `ThemeToggle` and `DensityToggle`.
  The theme follows the operating system until the user picks one. Signed in, the account
  decides: its values apply when a page opens and when the user returns to the window, and
  a change saves at once. The browser remembers the last values so pages open without a
  flash and pages before sign-in match. A failed save keeps the choice in this browser and
  shows a toast with a retry; the next change sends it too.
- Chart colors pass the dataviz palette validator in both themes before use.

## Errors and empty states

Every failure says what happened and what to do next ([research](../research/errors-and-empty-states.md)).
Show API failures with `ProblemNotice`: it offers Try again only for failures that may pass,
waits as long as the server asked, offers Reload the page when the page is out of date, and
lets the request ID be copied. A new API call must declare its codes in `x-error-codes`, and
each code needs `problem.<CODE>.title` and `.detail` in every catalog; tests enforce both.
Write the title as what happened and the detail as the next step, without apologies, blame
or jargon. An empty state names what will appear and the first action; a filtered list that
comes back empty offers to clear the filters.

## Interaction principles

1. **Decide for the user, allow overrides.** Offer a recommended choice, such as
   automatic model placement, and keep advanced options collapsed.
2. **Every problem carries its next step.** Alerts and errors state the likely cause
   and offer the fixing action directly.
3. **Common tasks are one step away.** Quick actions on the overview, single-key
   shortcuts and a command menu (`Cmd+K`) that can run actions, not only navigate.
4. **Prefer undo to confirmation.** Reversible actions complete immediately and offer
   undo; only irreversible actions ask for confirmation
   ([research](../research/undo-and-confirmation.md)). Pass `undo` to the toast; when it
   returns a promise, a refusal is reported with a retry. An action that cannot be recalled
   but harms nothing, like sending an email again, just happens. One that cannot be undone
   and matters if wrong uses `TypedConfirmation`: the person types the target's own name, and
   the title, button and consequence all name the action.
5. **Bulk work and copying are always at hand.** Multi-select with a bulk action bar,
   copy buttons next to identifiers, commands and URLs, row actions on hover.
6. **Guide newcomers without slowing experts.** A setup checklist and empty states that
   explain the next action; shortcuts and remembered filters for daily use.
7. **When something breaks, show the answer.** Diagnostics, exact commands and a
   downloadable diagnostic bundle sit next to the failure.

## Application frame

`AppFrame` in `web/packages/shell` is the same on every signed-in page. Add a page by
giving its navigation item a `to` in the application's `navigation.ts`; until then the
item shows disabled, as in the prototype. Page navigation and new actions appear in the
command menu from the same list. Below 1024 pixels the sidebar moves into a drawer.

The frame also watches the session: while someone is active it keeps the session going, and
once they stop it warns before the session ends, with one action to stay signed in
([ADR 0024](../decisions/0024-warning-before-a-session-ends.md)). A request the person did
not ask for, such as a poll, passes `background: true`; otherwise an open tab holds the
session forever.

The workspace switcher only chooses what the interface shows, remembered per user in
this browser. The server checks access to the workspace on every request.

## Page patterns

- **List pages:** filter bar, table and detail drawer, built from the shell's list pieces
  ([research](../research/console-list-pattern.md)). Declare the list's filters and columns
  as a `ListDefinition` and keep its state with `useListView`; the toolbar holds a
  `SearchField`, `Segmented` or `FilterSelect` choices, then `SavedViewsMenu` and
  `ColumnChooser` at the right. Saved views follow the account; the screen is remembered in
  this browser. Filter on the server, so paging stays correct.
  - The column that names a row is `locked` and opens the row's `Drawer`.
  - Rows an action applies to get a checkbox, and the header checkbox has three states.
    `useRowSelection` keeps only rows on screen; `BulkBar` appears at the foot of the page
    with the count, the actions and Clear (or Escape). A bulk action reports how many rows
    succeeded and keeps the failed ones selected for a retry.
  - An empty result says whether filters caused it and offers to clear them.
  - Filters, sort and the open drawer live in the address, so a link shows the same list
    (UX-114); `CopyLinkButton` copies it. Columns are not part of a link. Sort on the server,
    with `aria-sort` on the sorted column and its header as a button.
- **Long-running tasks:** step timeline with durations plus a live log; the task keeps
  running when the user leaves the page ([research](../research/long-task-pattern.md)).
  - `StepTimeline` lists steps in order. The running step carries `aria-current="step"`, and a state
    is never told by colour alone.
  - `LiveLog` is a named `role="log"` that a keyboard can reach. It follows its end only while
    scrolled to the end, offers pause, copy and download, and marks where a new attempt begins.
  - Pages poll with a cursor on the installation's interval, as background reads, and stop when the
    task has finished. A failed read keeps what is on screen and offers a retry.
  - The top bar's `TaskTray` lists the person's unfinished tasks from any page, and a toast says once
    when one ends, with a way back to it. Give an application's frame `tasks` to turn it on.
  - Never block a page on a long task, and never make someone stay to see it finish.
- **Charts:** a single series needs no legend; every chart offers a table view and a
  hover readout.

## Offline rule

The build writes a Brotli and a gzip copy of every text file over 1 kB, and the control plane
sends one in place of the file when the browser accepts it; nothing is compressed on a request.

Fonts, icons, help content and every other asset ship inside the product. Pages never
load anything from the internet. The license texts of the fonts and icons ship in each
build as `/assets/third-party-notices.txt`.

## Accessibility

Every page meets WCAG 2.2 AA in both themes. The browser tests run axe-core on every
page state in light and dark (`packages/shell/testing/accessibility-flows.ts`), and a unit
test in each application fails when a route has no state in its `e2e/pages.ts`. A new page
adds its states there: the empty, loading, error and filled views and each dialog or
drawer it opens.

Keyboard and screen-reader use is part of that standard, and
`packages/shell/testing/keyboard-flows.ts` checks it on the same page states:

- **Every control is reachable with Tab and shows focus.** The test walks each state with
  Tab and fails on a control that is off screen or looks the same focused and unfocused.
  Use the global focus outline; a control that removes it draws a ring of its own.
- **Menus move with the arrow keys** and close with Escape; dialogs, drawers and the
  command menu close with Escape. Each returns focus to what opened it.
- **Each page has one `h1` and names the browser tab.** `AuthLayout` and the frame set the
  tab from the page title; status pages title their notice as the heading.
- **Navigation moves focus to the new page's `h1`,** so a screen reader announces it and
  the next Tab starts in the page. A change of only the query or hash keeps focus, so
  filters and drawers stay usable. A skip link leads past the navigation.

Text needs 4.5 to 1 against what it sits on, so three tokens differ from the prototype:

- **Tertiary text** is darker (`#706a60`, dark `#969084`); the prototype's reached only
  3.1 to 3.6 to 1.
- **Accent, strong** fills anything that carries white text, such as primary buttons. It is
  the prototype's hover shade; white on the teal accent reaches only 3.7 to 1 (3.1 in
  dark), so the accent itself stays for marks, switches, meters and focus rings.
- **Critical text** is the status red adjusted for each theme; the status red stays for
  icons and fills.

A test holds every text token, critical text and white on the strong accent at AA on
every surface in both themes.
