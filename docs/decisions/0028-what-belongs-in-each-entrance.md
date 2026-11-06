# 0028: What belongs in each entrance

Date: 2026-11-06. Status: accepted.

## The problem

[ADR 0027](0027-entrance-addresses.md) decided that there are four entrances and how
they are addressed. It did not say what is *inside* two of them, and the gap showed:
the console grew to 34 pages across seven groups while the portal had two. Everything
landed in the console because the console was where pages were being written, not
because an administrator was the person who needed it.

That is backwards. Submitting a fine-tune, calling a model from code, opening a
notebook and checking what you spent are not administration. They are the daily work
of the people the platform exists for, and today they are only reachable through a
console those people should never be given an account on.

## The rule

**The entrance is chosen by who the person is, not by which subsystem the feature
belongs to.**

- **Admin console** — the people who *run* the platform. Capacity, policy, hardware,
  tenants, money, upgrades, incidents. Scope: the whole installation, every tenant.
  The verbs are allocate, limit, approve, repair, publish.
- **Portal** — the people who *use* the platform. Scope: their own workspace, inside
  the quota an administrator gave them. The verbs are submit, call, open, try, spend.

## The consequence most of the work follows from

**A capability usually appears in both entrances, with different scope and different
verbs.** Serving is the clearest case. In the console it is "which models are
published, on which capacity, and what happens when a node dies". In the portal it is
"which models can I call, here is the endpoint and a code sample, here is what my
calls cost". Same subsystem, two products.

This is not duplication to avoid. Trying to serve both audiences from one page is how
the console got to 34 items: every page grew an admin half and a user half, and the
user half was buried where its audience could not reach it.

Where a capability has only one audience it has only one home. Firmware is console
only. A personal API key is portal only.

## The portal's surface

Derived from the roadmap by asking, for each item, who performs it.

| Area | What the person does |
| --- | --- |
| Home | What I have running, what I spent, what needs me |
| Assistants and conversations | Chat with what has been published to me |
| Applications | Open and install the apps published to my workspace |
| Models | Browse what I may call, and try one in a playground |
| API and keys | My keys, the endpoint, code samples, my own call log |
| Jobs | Training, batch and scientific jobs I submitted, with logs and results |
| Workspaces | Notebooks and development environments I own |
| Data | My datasets and files, within my storage quota |
| Evaluation | Runs I started, and how my model scored |
| Usage and quota | Tokens, GPU hours and budget against what I was given |
| Help | Documentation, which ships inside the product |

## What this does not change

Audience binding stays exactly as it is: a portal session is refused at the console
and the reverse, every admin route is absent from the portal listener, and nothing is
inferred from a header. A portal page reads the person's own scope from the
authenticated session, never from a parameter the browser supplies, so "my jobs" is
enforced on the server, not by which page was opened.

## Navigation shape, for both

Research into enterprise consoles is thin on numbers, and the one sidebar-specific
figure found, 10 to 30 sections, is one consultancy's. The defensible reading across
sources is: few top-level groups, shallow nesting, no critical destination hidden in
a submenu.

- Group the sidebar, and keep the groups to about seven.
- One level of nesting under a group. Anything deeper becomes a tab on the page it
  belongs to, not a third level in the tree.
- Tabs carry the views of one object. The sidebar carries objects, not views.
- Databricks is a useful reference for grouping by product area with a single
  creation entry point rather than a create button on every page.

## Consequences

- The portal gains the items in `docs/product/roadmap/portal.md`.
- Console pages whose audience is a user keep their administrative half and lose the
  self-service half to the portal.
- Every new roadmap item names its entrance before it is built. An item that cannot
  name one has not been thought through.
