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
| Text, tertiary | `#8c8679` | `#868074` |
| Accent (teal) | `#0d9488` | `#14a394` |
| Status: good, warning, critical | `#0ca30c`, `#fab219`, `#d03b3b` | same |

- Spacing follows a 4-pixel scale. Radii: 8 px for controls, 12 to 14 px for cards,
  18 px for dialogs.
- Typography: Figtree, 14 px base, page titles 27 px at weight 750; JetBrains Mono
  for identifiers, code and logs. Numbers in tables use tabular figures.
- Icons: Lucide at 18 px with a 1.75 stroke, set once in the stylesheet; import them
  from `lucide-react` and mark decorative ones `aria-hidden`.
- Theme and density are per browser, chosen with `ThemeToggle` and `DensityToggle`.
  The theme follows the operating system until the user picks one.
- Chart colors pass the dataviz palette validator in both themes before use.

## Interaction principles

1. **Decide for the user, allow overrides.** Offer a recommended choice, such as
   automatic model placement, and keep advanced options collapsed.
2. **Every problem carries its next step.** Alerts and errors state the likely cause
   and offer the fixing action directly.
3. **Common tasks are one step away.** Quick actions on the overview, single-key
   shortcuts and a command menu (`Cmd+K`) that can run actions, not only navigate.
4. **Prefer undo to confirmation.** Reversible actions complete immediately and offer
   undo; only irreversible actions ask for confirmation.
5. **Bulk work and copying are always at hand.** Multi-select with a bulk action bar,
   copy buttons next to identifiers, commands and URLs, row actions on hover.
6. **Guide newcomers without slowing experts.** A setup checklist and empty states that
   explain the next action; shortcuts and remembered filters for daily use.
7. **When something breaks, show the answer.** Diagnostics, exact commands and a
   downloadable diagnostic bundle sit next to the failure.

## Page patterns

- **List pages:** filter bar, table and detail drawer.
- **Long-running tasks:** step timeline with durations plus a live log; the task keeps
  running when the user leaves the page.
- **Charts:** a single series needs no legend; every chart offers a table view and a
  hover readout.

## Offline rule

Fonts, icons, help content and every other asset ship inside the product. Pages never
load anything from the internet. The license texts of the fonts and icons ship in each
build as `/assets/third-party-notices.txt`.

## Known gaps

White text on the accent color has a contrast of 3.7 to 1 in the light theme and 3.1 to 1
in the dark theme, below the 4.5 to 1 that WCAG AA asks of 14-pixel text, and tertiary
text is below it too. Both are the approved
palette; UX-112 decides how to close the gap. A test holds primary and secondary text
and `accent-ink` at AA on every surface in both themes.
