# The interaction system

Date: 2026-11-07.

Read this before designing or building any screen. It is the part that was missing:
the roadmap said what to build, and nothing said how it should behave, so each
screen was decided again from scratch and the product came out looking like a pile
of pages rather than one thing.

Three documents, and they are read in this order:

1. **This one** — how a screen behaves. Surfaces, archetypes, states, motion.
2. [`../plan/README.md`](../plan/README.md) — what gets built, in what order, with each
   feature's screens, dialogs and click paths specified.
3. [`../roadmap/`](../roadmap/) — the existing 530-item engineering ledger. Still the
   record of what is done; it answers *what*, this answers *how*.

## 0. The one rule

**Every control must be worth the click.** A screen is not finished because the data
is on it. It is finished when the thing a person came to do takes the fewest
decisions, and the next thing they will want is already within reach.

Two tests, applied to every screen before it is called done:

- **The first-minute test.** Someone who has never seen this screen: can they do the
  most common thing without reading anything but the labels?
- **The thousandth-time test.** Someone who does this daily: is there a faster path
  than the one a beginner takes? Keyboard, bulk action, saved view, a pin.

A screen that fails the first test is cluttered. A screen that fails the second is a
toy. Enterprise software has to pass both, and that is the whole design problem.

## 1. References, and what is actually usable from them

Four resources were reviewed on 2026-11-07. Honest assessment, because two of them
turned out not to apply:

| Source | What it is | What we take |
| --- | --- | --- |
| [Arc Library](https://github.com/kuratlielia/arc-library) | MIT React component library, 121 components, CSS modules on semantic tokens | The most usable of the four: the semantic token list, one-line definitions of what each surface is *for*, "status, always paired with a label", dark mode tuned separately rather than inverted, a reduced-motion path for every animation |
| [Taste Skill](https://github.com/Leonxlnx/taste-skill) | An agent skill for building marketing landing pages | Partly transferable: one button pair, one card style, one radius scale, equal content edges, nothing scroll-linked, reduced motion that rests without hiding, and checking the layout in a real browser at several widths. Its page-shape rules are for marketing sites and do not apply to a console |
| [Oversight Supply](https://supply.madebyoversight.com/) | A Webflow/Framer template shop | Nothing. It is a catalogue with no stated principles |
| [Curated](https://curated.design/) | A gallery of 2,926 sites, filed by industry | Nothing prescriptive. Useful only as a place to look at work, not as a source of rules |

Earlier research already in force: progressive disclosure
([ADR 0037](../../decisions/0037-simple-on-the-surface-deep-underneath.md)), disabled
states and capability declaration
([ADR 0036](../../decisions/0036-show-every-option-and-fetch-models-offline.md)),
entrance separation ([ADR 0027](../../decisions/0027-entrance-addresses.md)) and what
belongs in each ([ADR 0035](../../decisions/0035-what-belongs-in-each-entrance.md)).

## 2. Which surface, for which job

This is the table that stops the guessing. Pick the surface from the job, never from
how much content there happens to be.

| Surface | Use it when | Never use it for | Dismiss |
| --- | --- | --- | --- |
| **Page** | The person navigated here on purpose. It has an address worth sharing. | Something that interrupts another task | Navigate away |
| **Drawer** (side panel) | Looking at or editing *one row* without losing the list behind it | A decision that must be answered before anything else | Escape, click outside, close button |
| **Dialog** (modal) | A decision that needs an answer before the task can continue, or a short create form of five fields or fewer | Browsing. Reading. Anything with its own sub-navigation | Escape, cancel; **never** by clicking outside when it holds unsaved input |
| **Inline expand** | Supporting detail that belongs in place, where losing the surroundings would cost context | A form long enough to scroll | Click the same control again |
| **Toast** | Confirming something that already finished, that the person can forget about | Errors they must act on. Anything carrying a decision | Itself, after about 5 seconds |
| **Alert** (in place) | A condition that persists and that the person has to resolve or acknowledge | A transient success | Resolving the condition |
| **Full-page takeover** | A flow with steps, where the surroundings would be a distraction: install, first-run, migration | Anything under three steps | Finish or cancel, with an explicit exit |

Rules that follow:

- **Never a dialog on top of a dialog.** If a dialog needs a dialog, the flow is a
  page.
- **A drawer may be opened from a list; a dialog may not open a drawer.** One depth.
- **A toast never carries the only copy of information.** If it matters in a minute,
  it belongs in the record, not in a message that disappears.
- **A destructive action confirms by typing the resource's name**, not by clicking
  "OK". Clicking OK is muscle memory; typing is a decision. Hold-to-confirm is an
  acceptable alternative for a single frequent destructive action.
- **Anything reversible offers undo instead of confirmation.** Confirmation is a tax
  on the ninety-nine times nothing was wrong. Undo is a refund on the one time it was.

## 3. The five page archetypes

Every page is one of these. If a page is not one of these, either it is two pages or
the archetype list is wrong; both are worth stopping over.

### 3.1 Overview
Answers three questions in order: **what is broken, what is running, what did it
cost.** Never a wall of charts.

Anatomy: title → *needs you* list (empty when nothing does, and that is a good day) →
four summary tiles at most → two or three cuts of recent activity → nothing else.

### 3.2 List
The working surface of the console. Most time is spent here.

Anatomy: title + one primary action → tabs, if the same objects have genuinely
different views → **summary tiles that filter the list below them** → filter bar
(search, facets, saved views, column control) → table → pagination.

- Tiles are filters, not decoration. Clicking "Quarantined 1" filters to it.
- The filter state lives in the address, so a filtered list can be sent to a colleague.
- Row click opens the drawer. The row's name is a link to the detail page.
- Row actions: at most two inline, the rest behind an overflow menu.
- Bulk actions appear only when something is selected, in a bar that replaces the
  filter bar rather than appearing on top of it.

### 3.3 Detail
One resource, everything about it. **A drawer is a preview; the detail page is the
real thing**, and every list must have one.

Anatomy: breadcrumb → name + status + the two actions used most → tabs (Overview,
Configuration, Activity, and what the resource needs) → related resources.

Every detail page has an **Activity** tab. Who changed what, when, and from where.

### 3.4 Flow
Creating or changing something that takes steps.

- Three steps or fewer: one page, sections, one submit.
- Four or more: a stepper, with every step reachable backwards and a review before
  commit.
- **Always show the cost before the commit**: what it will consume, what it will
  cost, what it will interrupt.
- A flow is resumable. Closing it keeps a draft.

### 3.5 Settings
Grouped, saved per section, never one giant Save at the bottom.

Each setting states its default, and where it takes effect. A setting that needs a
restart says so beside itself, not in a paragraph at the top.

## 4. States every screen must define

A screen is not designed until all seven are written down. Most of the bugs people
call "rough edges" are one of these being missing.

| State | The rule |
| --- | --- |
| **Loading** | Skeletons that match the real layout, never a spinner on a blank page. Under 300 ms, show nothing: a flash is worse than a wait |
| **Empty, first time** | Say what this is for and give the one action that fills it. Never an empty table with headers |
| **Empty, filtered** | Different from the above. Say which filter is responsible and offer to clear it |
| **Partial** | Some data arrived, some did not. Show what came and name what is missing. Never fail the whole page for one widget |
| **Error** | What happened, whether it is the person's to fix, and the next step. Never a code alone; never "something went wrong" alone |
| **Denied** | Say the permission is missing and who grants it. Never hide a destination the person's role genuinely has: hiding makes a product look smaller and makes the person ask anyway |
| **Not ready** | The capability exists but not in this release. Visible, `aria-disabled`, with a reason ([ADR 0036](../../decisions/0036-show-every-option-and-fetch-models-offline.md)) |

## 5. Tokens

Semantic names only. A component never names a raw colour. Changing a token changes
the product; changing a hex in a component is a bug.

```
--background  --surface  --surface-2  --surface-3
--text  --text-2  --text-3  --deemph
--border  --border-strong
--accent  --accent-hover  --accent-soft  --accent-ink
--good  --warning  --critical
--radius-s (8)  --radius-m (12)  --radius-l (14)
--row (52 comfortable / 40 compact)  --gap (20 / 14)
```

- **Dark mode is tuned, not inverted.** Separate values, checked for contrast.
- **Status is never colour alone.** Every status carries a word; the dot is a
  reinforcement. This is not only an accessibility rule — a screenshot in a report
  prints in grey.
- **One radius scale, one button pair** (primary and secondary; danger is a
  modifier), **one card style.** If a screen needs a second of any of these, the
  screen is wrong.

## 6. Motion

- Under 200 ms for anything that happens on a click. 150 ms for hover.
- **Nothing scroll-linked.** No fade-in, no parallax, no reveal. Data must be
  readable the instant it is on screen.
- **Every animation has a reduced-motion path**, and under reduced motion everything
  rests. Nothing stays hidden because its reveal did not run.
- Progress that takes longer than two seconds shows what step it is on, not a bar
  that lies.

## 7. Density and the expert path

The console is used all day by people who know it. Alongside the beginner path:

- **Keyboard**: `⌘K` command menu everywhere; `/` focuses search; `j`/`k` move the
  row selection; `Enter` opens; `Escape` closes one layer.
- **Density** switches row height and gap, and is remembered per person.
- **Saved views** on every list: filter, sort, columns, shareable by link.
- **Pinned destinations** at the top of the navigation, the person's own choice.
- **Bulk** on every list that can have a hundred rows.

## 8. What is checked automatically

A rule nobody checks is a rule that decays. These are enforced, and new rules join
this list rather than living only in prose:

- No Chinese outside the content directory; every string localized.
- Every roadmap item has a page; every navigation target resolves.
- No unavailable option without a reason and `aria-disabled`.
- Neither entrance scrolls at 1440×900.
- Every page and tab opens, and every button on it can be pressed, with no console
  error.
- Nothing is requested from the internet.

To add: contrast of every token pair, one-radius-scale and one-button-pair checks,
a real-browser layout check at 1440, 1280 and 768, and a check that every list has a
detail page and every screen defines its empty state.
