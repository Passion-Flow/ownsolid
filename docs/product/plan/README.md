# The build plan

Date: 2026-11-07. This replaces working from the engineering ledger alone.

## Why this exists

The [roadmap](../roadmap/) has 530 items and every one of them is real. What it does
not have is any statement of how a thing should behave, so every screen got decided
again while it was being written, and the result was a pile of pages instead of one
product.

This plan fixes that by changing the unit of work. **The unit is a screen, not a
capability.** A screen is specified before it is built: who it is for, the one thing
it exists to do, what is in the list, what happens on click, which dialogs exist,
and what it looks like when there is nothing there.

Read [`../design/README.md`](../design/README.md) first. It defines the surfaces, the
five archetypes and the seven states this document refers to throughout.

## How an entry is written

```
### ID  Name                                          `#/route`
For        who opens it
Archetype  overview | list | detail | flow | settings
Does       one sentence, in the user's words
Primary    the single most important action on the screen
Content    columns, sections, whatever the archetype needs
Click      what each thing does, and where it goes
Dialogs    every dialog and drawer, with its surface chosen from the table
Empty      the first-time empty state, and the filtered-empty state
Done when  the condition, testable
```

An entry that cannot name its **Primary** is not a screen yet; it is a pile of
controls looking for a purpose. An entry whose **Empty** is "a table with no rows" is
not finished.

## The order, and why

Ordered by what a customer can do at the end of each stage, not by subsystem. Each
stage ends with something demonstrable to a buyer.

| Stage | At the end, a customer can | Depends on |
| --- | --- | --- |
| **1. See the truth** | Sign in, see the fleet honestly, know what this release supports | — |
| **2. Serve a model** | Bring a model in offline and get it answering on their own GPUs | 1 |
| **3. Hand it to their people** | Give colleagues a portal, a key and a playground | 2 |
| **4. Keep control** | Divide capacity, set budgets, prove who did what | 3 |
| **5. Run it** | Train, evaluate, observe, back up, upgrade | 4 |

Stages 1 and 2 are specified in full below. Stages 3 to 5 list their screens at the
same granularity but are expanded to full entries before each stage starts, because
specifying a screen a month early is specifying it twice.

---

# Stage 1 — See the truth

### S-101  Sign in                                    `#/sign-in`
**For** everyone, at either entrance ·
**Archetype** flow (one step) ·
**Primary** sign in.

**Does** lets a person in, and makes it obvious which entrance they are at.
**Content** entrance panel (console only), email, password, demo credentials where
the deployment enables it.
**Click** forgot password → `#/forgot-password`, keeping the address they wanted in
`?next=`. Sign in → where they were going, or the entrance home.
**Dialogs** none. A sign-in screen that opens a dialog is wrong.
**Empty** not applicable.
**Done when** server-side session, audience-bound cookie, CSRF, rate limit, no
account enumeration. **Built.**

### S-102  Console overview                           `#/overview`
**For** platform administrators · **Archetype** overview · **Primary** go to whatever
is broken.

**Does** answers what is broken, what is running, what it cost — in that order.
**Content** *needs you* list → four tiles (nodes ready, GPUs allocated, deployments
healthy, spend this month) → recent activity → nothing else.
**Click** each *needs you* row goes straight to the thing, never to a list containing
the thing.
**Dialogs** none; this screen only routes.
**Empty** "Nothing needs you." with the fleet summary still shown. A quiet overview
is the product working.
**Done when** every row resolves to a real destination and the page renders with no
data at all.

### S-103  Nodes                                      `#/fleet`
**For** operations · **Archetype** list · **Primary** add nodes.

**Content** tiles: ready / quarantined / draining / joining — **each one filters the
table.** Columns: name, status, hardware, GPU use, memory, driver and CUDA, pool.
**Click** row → drawer preview. Name → `#/node/<id>`. Inline: terminal, drain.
Overflow: cordon, reboot, relabel, remove.
**Dialogs**
- *Add nodes* → flow S-104, a page not a dialog; it has steps.
- *Drain* → dialog. Says how many pods move and how long it will take. Reversible, so
  it offers undo rather than a typed confirmation.
- *Remove* → dialog, typed confirmation on the node name, and it refuses while work
  is running rather than asking twice.
**Empty** first time: "No nodes yet" with *Add nodes*. Filtered: name the filter,
offer to clear.
**Done when** tiles filter, filter state is in the address, bulk drain works on a
selection, and the detail page exists.

### S-104  Add nodes                                  `#/fleet/enroll`
**For** operations · **Archetype** flow · **Primary** issue a token.

**Content** step 1 what the node runs (OS and accelerator, from the capability
registry, unavailable ones shown with reasons) → step 2 pool and labels → step 3 the
install command, or a CSV for many.
**Click** copy the command. Watch nodes appear as they join, on this screen.
**Dialogs** *Revoke token* → dialog with typed confirmation.
**Empty** not applicable.
**Done when** a node that joins appears here without a refresh, and a CSV of 48 shows
per-rack progress.

### S-105  Node detail                                `#/node/<id>`
**For** operations · **Archetype** detail · **Primary** drain.

**Content** name, status, the two most-used actions. Tabs: Overview (hardware, GPUs,
what is scheduled here), Metrics, Activity, Configuration.
**Click** a GPU goes to the GPU's own view. A workload goes to its deployment.
**Dialogs** as S-103, plus *Run a command* → drawer with a terminal.
**Empty** a node with nothing scheduled says so, with its capacity.
**Done when** Activity shows who did what and from where.

### S-106  What is supported                          `#/support`
**For** buyers and operators · **Archetype** list · **Primary** none; it is a reference.

**Does** every option the product offers, and which work in this release.
**Content** one table per registry: operating systems, accelerators, engines,
catalogue sources, acquisition modes. Columns: option, state, reason, roadmap item.
**Click** a roadmap item links to the public item.
**Dialogs** none.
**Empty** not applicable.
**Done when** it is generated from the capability registry, so it cannot drift from
what the forms offer. **Built.**

---

# Stage 2 — Serve a model

### S-201  Model market                               `#/market`
**For** anyone who needs a model · **Archetype** list · **Primary** add to the fetch list.

**Content** tabs: *Your models* (empty on a new installation, and says so), *Catalogue*
(the offline index across Hugging Face and ModelScope), *Fetch list*, *Index*.
Catalogue columns: model, publisher, parameters, context, licence, weights, registry.
Facets: task, licence class, size band, registry, language.
**Click** row → drawer: what it needs here (weights, GPU memory at the chosen context,
which cards it fits, which engines can serve it). Name → `#/model/<id>`.
**Dialogs**
- *Add to the fetch list* → toast. Reversible, no confirmation.
- *Import a bundle* → flow, with hash verification shown per model.
- *Refresh the index* → dialog, source chosen from the capability registry.
**Empty** *Your models* first time: "No models yet", with *Search the catalogue* and
*Import a bundle*. Catalogue filtered to nothing: name the facet.
**Done when** search works with the network disconnected and a fetch manifest exports.

### S-202  Model detail                               `#/model/<id>`
**For** anyone · **Archetype** detail · **Primary** deploy.

**Content** name, publisher, licence, size. Tabs: Overview (what it is for, what it
needs, its files), Versions and lineage, Deployments using it, Activity.
**Click** *Deploy* → S-203 with the model already chosen.
**Dialogs** *Retire* → dialog; refuses while a deployment or a key depends on it, and
lists what does.
**Empty** a model with no deployments says so and offers to make one.
**Done when** the licence is shown as the registry states it, unreinterpreted, and
lineage reaches back to the bundle it arrived in.

### S-203  Deploy a model                             `#/serving/new`
**For** platform administrators · **Archetype** flow · **Primary** deploy.

**Content** pick a model → the recommendation, in words ("2 replicas on infer-a, one
GPU each, chosen from the model and your free capacity") → **advanced fold**: name,
replicas, engine (from the capability registry), autoscaling, read-only root, egress.
**Click** deploy runs a task with steps and a live log, and the task survives leaving
the page.
**Dialogs** none before the commit; the review *is* the page.
**Empty** no models: send to S-201 rather than showing an empty picker.
**Done when** the cost is shown before the commit: GPUs taken, memory, what is left
after.

### S-204  Deployments                                `#/serving`
**For** platform administrators · **Archetype** list · **Primary** deploy a model.

**Content** tiles: healthy / degraded / scaling / stopped, each filtering. Columns:
deployment, model, engine, replicas, requests per minute, p95 latency, state.
**Click** row → drawer with live replicas. Name → `#/deployment/<id>`.
**Dialogs** *Scale* → dialog, shows the capacity it takes before applying. *Stop* →
dialog, typed confirmation, names the keys and apps that will start failing.
**Empty** "Nothing deployed" with *Deploy a model*.
**Done when** a rolling update is visible replica by replica.

### S-205  Deployment detail                          `#/deployment/<id>`
**For** platform administrators · **Archetype** detail · **Primary** scale.

**Content** tabs: Overview (replicas, placement, health), Traffic (requests, latency,
errors, token throughput), Logs, Configuration, Activity.
**Click** a replica → its node. An error → the log line that produced it.
**Dialogs** *Roll back* → dialog showing the difference between versions.
**Done when** Traffic and Logs line up on the same clock.

### S-206  Gateway keys                               `#/gateway`
**For** platform administrators · **Archetype** list · **Primary** issue a key.

**Content** columns: name, prefix, scope, owner, last used, expiry. Tiles filter by
expiring soon / unused / over limit.
**Click** row → drawer with this key's call log.
**Dialogs** *Issue* → dialog, five fields, shows the key **once**. *Rotate* → dialog
stating the overlap window. *Revoke* → typed confirmation naming what breaks.
**Empty** "No keys yet" with what a key is for.
**Done when** a key's scope is enforced and visible, and the call log is per key.

---

# Stage 3 — Hand it to their people

Screens, to be expanded before the stage starts:

| ID | Screen | Archetype | Primary |
| --- | --- | --- | --- |
| S-301 | Portal home `#/portal-home` | overview | continue what you were doing |
| S-302 | Conversations `#/portal-chat` | list + detail | new conversation |
| S-303 | Applications `#/portal-apps` | list | open an app |
| S-304 | Models for users `#/portal-models` | list | try a model |
| S-305 | Playground `#/portal-models/playground` | flow | run |
| S-306 | My API keys `#/portal-keys` | list | new key |
| S-307 | My usage `#/portal-usage` | overview | — |
| S-308 | My account `#/portal-account` | settings | change password |
| S-309 | App catalogue, admin side `#/catalog` | list | publish an app |

The portal's rule, from [ADR 0035](../../decisions/0035-what-belongs-in-each-entrance.md):
every screen is scoped to one person's workspace, enforced on the server, and nothing
here manages the platform.

# Stage 4 — Keep control

| ID | Screen | Archetype | Primary |
| --- | --- | --- | --- |
| S-401 | Tenants and workspaces `#/tenants` | list | create a workspace |
| S-402 | Workspace detail `#/workspace/<id>` | detail | adjust quota |
| S-403 | Quota tree `#/scheduling` | detail | move capacity |
| S-404 | Budgets `#/billing` | list | set a budget |
| S-405 | Usage ledger `#/billing/ledger` | list | export |
| S-406 | People and roles `#/iam` | list | invite |
| S-407 | Audit log `#/audit` | list | export a range |
| S-408 | Sessions and devices `#/iam/sessions` | list | end a session |

# Stage 5 — Run it

| ID | Screen | Archetype | Primary |
| --- | --- | --- | --- |
| S-501 | Fine-tuning `#/training` | list | new run |
| S-502 | Run detail `#/run/<id>` | detail | compare |
| S-503 | Datasets `#/data` | list | import |
| S-504 | Evaluation `#/evaluation` | list | new evaluation |
| S-505 | Monitoring `#/observability` | overview | — |
| S-506 | Alerts `#/observability/alerts` | list | new rule |
| S-507 | Backup and restore `#/reliability` | list | back up now |
| S-508 | Upgrade `#/delivery` | flow | upgrade |
| S-509 | Installation settings `#/settings` | settings | — |

---

## What happens to the 530-item roadmap

It stays. It is the engineering ledger and the record of what is actually done, with
its evidence links and its tick rule. Nothing is deleted.

The change is that **a roadmap item is no longer enough to start work.** An item that
touches a screen waits for that screen's entry here. The two meet in the done
condition: the roadmap item is ticked when its code is merged and tested; the screen
is done when it also behaves the way this document says.

## What this plan deliberately does not do

- It does not promise dates. Stage boundaries are capability boundaries.
- It does not cover the model gateway's API surface or the agent protocol. Those have
  no screens; they stay in the ledger and in `api/`.
- It does not re-specify sign-in, the support matrix, or the capability registry.
  Those are built and marked so above.
