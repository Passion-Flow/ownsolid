# Console overview

Research behind UX-101: the page an administrator lands on, with quick actions, what needs attention,
a setup checklist and fleet health.

## How others solve it

- **Portainer** ([Home](https://docs.portainer.io/user/home)): the first page after sign-in is a
  card view of environments "with at-a-glance health and performance metrics". A header bar breaks
  the fleet down by health, and clicking a segment filters the list. Card icons act directly.
- **GitLab** ([Admin area](https://docs.gitlab.com/administration/admin_area/)): an Instance overview
  of counts first; health views are separate pages reached by navigation, so a figure does not lead
  to its fix.
- **AWS Console Home** ([getting started](https://docs.aws.amazon.com/awsconsolehelpdocs/latest/gsg/getting-started.html)):
  widgets such as Recently visited and AWS Health, which a person arranges.
- **Nielsen Norman Group** ([dashboards](https://www.nngroup.com/articles/dashboards-preattentive/)):
  an operational dashboard exists for quick, time-sensitive decisions and focuses on a single task.
  Position and length read best for quantities; colour is a secondary cue.

None of these publishes an attention list or a checklist that completes itself. Those come from the
owner's approved prototype, and the rules below are ours.

## Choices

- **Follow the prototype's order, and only with what is real.** Quick actions, health tiles, Needs
  attention beside Finish setup. Accelerators, model services, gateway traffic, installed apps and
  the air-gap card have no data yet and arrive with their modules ([ADR 0031](../decisions/0031-web-lane-first-module-pages.md)
  says the overview links to pages, not to dead ends). So the quick actions are adding a node,
  inviting someone, importing a license and seeing tasks.
- **Each attention item carries its next step,** as a link that opens the fix: the offline node's
  drawer, the failed tasks, the failed emails, the License page. Items sort by what stops work.
- **Attention is computed in the browser from the lists that exist,** so no server change and no
  second opinion of what "offline" means. Each source loads on its own, and one that fails says so
  with a retry and leaves the others on screen. An operator with a failing mail table still sees
  an offline node.
- **Only active nodes need attention.** An administrator who disables a node and shuts it down has
  dealt with it. The server counts a disabled node under its connection state as well, so the fleet
  card says that; its rows are not meant to add up to the total.
- **A license read is not polled like a list.** It verifies the signed history and writes the time
  under a lock, so the overview reads it once a minute, as the sidebar does, and setup state on load.
- **Recent means the last 24 hours** for failed tasks and emails. A failure stays in its list for
  good, and a resent email leaves the failed one behind, so an unbounded count would never clear.
  The text says "in the last 24 hours", and the link shows the whole list.
- **The checklist completes itself from real state** and disappears when finished: an administrator
  never ticks a box, and an expert stops seeing it. A step that has no page yet, such as company
  sign-in or nightly backups, joins when its page does.
- **Fleet health is rows with a length and a count,** not a donut: each state is a link to the
  filtered list, as Portainer's segments are, and a state is told by its name, never by colour alone.
- **The page polls as a background read,** so an open overview keeps no session alive.
- **The heading stays "Welcome, name".** The prototype's "Good afternoon" depends on the browser's
  clock and time zone to say nothing useful; the sentence under it carries the information.
- **Single-key shortcuts wait for UX-122.** The prototype shows them, but WCAG 2.2's 2.1.4 needs a way
  to turn off or remap a one-character shortcut, which needs an account preference.

Rejected: a server summary endpoint, which would duplicate rules the list endpoints own, and may be
worth it once the sources outgrow one screen; a checklist a person dismisses, which hides the work.

## Licences

No new dependency.

## Failure modes and tests

- One source failing beside healthy ones: a browser flow per source, with its retry.
- Everything clear: an empty state that says so, not a blank card.
- A fleet with more offline nodes than are named: the rest are one link to the filtered list.
- A node disabled on purpose and shut down: it stays out of the attention list.
- More failures than one page holds: the count says "or more".
- A polled refresh failing: what is on screen stays.
- A new installation: the checklist's steps follow real state, and the card goes when all are done.
- Quick actions reach the dialog they name; a browser flow each.

## What done means

After sign-in an administrator sees, on real data, how many nodes are online, what needs attention with
a link to the fix, what is left to set up and the fleet by state, and reaches adding a node, inviting
someone and importing a license in one step, in both languages, with loading, empty and error states.
