# 0030: Simple on the surface, deep underneath

Date: 2026-11-07. Status: accepted.

## The requirement

An enterprise product has to be simple enough to use on the first day and deep
enough to still fit on the thousandth. Those pull in opposite directions, and a
product that picks one loses: too shallow and a serious customer outgrows it in a
quarter; too deep and nobody gets past the first screen.

This is not a new problem and it has a settled answer. Progressive disclosure was
formalised at IBM in the 1980s and has been the standard treatment since: show the
most-used part first, make the way to more obvious, and keep the hiding shallow.

## What was wrong

Three faults, and all of them made every page feel like the same page.

**The navigation was a wall.** Thirty-four destinations, every one visible, always.
Seven group headings that labelled the wall without reducing it. You scrolled the
navigation, and you could not tell where in the product you were.

**Nothing was progressively disclosed.** Forms showed every field flat, so the
simple case paid the full price of the complex one.

**Nothing was the person's own.** No pinning, no density choice that mattered, no
way to make a console you use daily fit the way you work.

## The decisions

### Navigation

- **Groups collapse, and only the group you are in is open.** A wall of thirty-four
  becomes about seven headings plus wherever you are.
- **Open state self-cleans.** A group is open because it holds the current page, or
  because the person opened it. Without the second half, every group ever visited
  stays open and the wall rebuilds itself over a session.
- **A group heading has exactly one behaviour: it opens and closes.** It never
  navigates. Mixing the two is what makes a sidebar feel unpredictable.
- **Pinned destinations sit at the top**, chosen by the person. This is the
  customization that matters most and costs least.
- **A filter box, and the command menu.** Search is the expert's form of
  disclosure: it skips the hierarchy instead of walking it.
- **The current destination is marked by a bar and by weight, never by colour
  alone**, and it stays marked after the pointer leaves.
- **Creation actions are not destinations.** A destination opens something that
  exists; a creation action starts a task, and belongs on the page it creates into.
- **Collapsed mode is icon-only but never label-less.** Every item keeps its
  accessible name, because an icon alone is ambiguous for product-specific areas.

### Forms

- **One fold, never two.** The common path first; everything with a working default
  behind `Advanced settings`. The research is explicit that burying things two or
  three levels deep trades one problem for another.
- **The fold says how much is behind it**, so the person can judge whether to open
  it without opening it.
- **A field is advanced when it has a default that works.** If leaving it alone can
  produce a wrong result, it is not advanced, it is required.

### What is not hidden

Progressive disclosure fails when something essential goes behind the fold. Three
things never do: anything without a safe default, anything that costs money or
capacity, and anything that cannot be undone.

## Consequences

- `web/apps/console` and `web/apps/portal` share one navigation component built to
  this shape; the demo's `src/app.tsx` is the working reference.
- Every form field declares whether it is advanced. The default is not advanced, so
  a new field is visible until someone decides otherwise.
- Pinning, navigation collapse and density are per-person preferences, stored for
  that person and never shared.

## Still open

Two things this decision names but does not yet deliver, both tracked on the
roadmap: summary tiles above a list should filter it rather than decorate it, and
list pages need a real detail view with its own tabs rather than only a drawer.
