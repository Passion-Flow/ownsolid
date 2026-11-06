# Console navigation

Research behind UX-123: the sidebar that [ADR 0037](../decisions/0037-simple-on-the-surface-deep-underneath.md)
asks for. The ADR decides the shape; this note records how it is built and tested.

## How others solve it

- **WAI-ARIA disclosure navigation**
  ([example](https://www.w3.org/WAI/ARIA/apg/patterns/disclosure/examples/disclosure-navigation/)): a real
  button with `aria-expanded` and `aria-controls`, links in a list nested with it, `aria-current="page"` on
  the current link, Space or Enter toggling. It "does not use the WAI-ARIA menu role", because a site's
  navigation is links rather than an application menu.
- **Carbon UI shell**
  ([left panel](https://carbondesignsystem.com/components/UI-shell-left-panel/usage/)): "Sub-menus are denoted
  with a chevron and expand when clicked", collapse only "when the user again clicks the menu header", and two
  tiers at most, which is what this console has.
- **AWS console**
  ([getting started](https://docs.aws.amazon.com/awsconsolehelpdocs/latest/gsg/getting-started.html)): a
  person's favorites and recently visited consoles beside the list of services, the reason UX-124 follows this.

## What the standards require

- [WCAG 1.4.1](https://www.w3.org/WAI/WCAG22/Understanding/use-of-color.html): colour "is not used as the only
  visual means" of showing state, so the current page has a bar and weight, and in the rail the license shows
  an icon rather than only a coloured dot.
- [WCAG 4.1.2](https://www.w3.org/WAI/WCAG22/Understanding/name-role-value.html), failure F89: a link whose
  only content is an image needs a name, so each rail item keeps its label as text for assistive technology.
- [WCAG 1.4.13](https://www.w3.org/WAI/WCAG22/Understanding/content-on-hover-or-focus.html): content shown on
  hover or focus can be dismissed, can be hovered, and stays. The rail's name does all three.
- [WCAG 4.1.3](https://www.w3.org/WAI/WCAG22/Understanding/status-messages.html): the filter's result count
  is a status message.

## Choices

- **A button and a list, as the pattern has it.** The heading never navigates (ADR 0037).
- **What a person toggled lasts until the page changes**, held in memory. A query string does not count as a
  new page, or a filter on a list would close a group the person just opened. This is the self-cleaning the ADR
  asks for: the state cannot build up, because nothing about it is stored.
- **A closed group holding the current page says so**, with the bar, heavier text and `aria-current="true"`,
  so a person who closes it can still tell where they are.
- **The filter matches the translated label, anywhere in it.** While it holds text, headings are plain text
  because a filter decides what shows. Enter opens the first page that exists, never one that has not shipped.
  Escape clears it and changing page clears it. It sits in the sidebar, outside `main`, so the `/` shortcut
  still means the page's own search.
- **The rail shows every destination, flat, in the groups' order with a rule between them.** Fifty-six pixels
  wide. A group's list has its heading as its accessible name. It lives on the account in
  `navigation_collapsed`, and in this browser so it applies before the account loads, like density. A drawer
  has room and is opened on purpose, so it is never a rail.
- **The name in the rail is drawn by the console**, because `title` never appears on keyboard focus. It is
  `aria-hidden`, since the link already carries the name, and `position: fixed` so the sidebar's scrolling
  does not clip it. React sets its position through the DOM, which the Content-Security-Policy allows.
- **No heading or filter when a navigation has no headings**, as the portal's does not.

Rejected: an accordion that closes the last group when another opens, which fights a person comparing two
groups and which the ADR does not ask for; storing open groups on the account, which would rebuild the wall;
a separate flyout per group in the rail, which adds a hover menu to reach what the command menu reaches faster.

ADR 0037 calls for pinned destinations at the top. They are UX-124 and go above this filter.

## Licences

No new dependency. The icons are Lucide (ISC), already bundled.

## Cost

The first download reached its 160 kB limit with this item: most of what grows there is the English catalog
and the shared stylesheet. The limit rises to 170 kB, and UX-129 moves the catalog out.

## Failure modes and tests

- Nothing about the current page is stored, so a stale choice cannot hide it; browser flow.
- A group opened by hand goes on the next page, and stays through a list filter; browser flow.
- Upgrading leaves everyone's sidebar open without a new revision; Go test on PostgreSQL.
- Two browsers changing different fields: an update names only its field; Go test and browser flow.
- MySQL and Dameng chains lack the column: they read as open and refuse collapsing; Go test.
- A name in Chinese is matched as shown; browser flow.
- Keyboard: every control reachable with visible focus, in both modes; browser flow and axe in both themes.
- The tip under the installation's Content-Security-Policy; live run.

## What the finished item must do

Sign in and see only the group in use open; open another with a heading; move on and see it close; find any
page by typing part of its name; collapse to icons, have every name still announced and shown on focus or
hover, and find the choice in another browser.
