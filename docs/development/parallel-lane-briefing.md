# Briefing for a parallel lane

Read this completely before writing anything. Every parallel session starts here,
whichever lane it was given. Your task message names the lane and the item; this
page is everything that is true regardless of which lane you got.

## 1. What this product is

ownsolid is a **fully offline, self-hosted AI infrastructure platform**. A customer
installs it in their own building, on their own machines, and runs large language
models on up to ten thousand GPU servers with no connection to the internet.

Four facts that change how you write code here:

1. **It is air-gapped.** Once installed, the machines cannot reach the internet.
   Nothing you write may download anything at runtime, call an external service, or
   send telemetry. Dependencies are packaged before delivery, never fetched.
2. **The customers are regulated.** State-owned enterprises, banks, drug companies.
   They have to prove who did what and when. Audit, permissions and traceability
   are requirements, not extras.
3. **There are two entrances**, with separate hostnames and sessions that do not
   cross: an admin console for operators and a portal for ordinary employees.
4. **One person owns this product.** Nobody will catch your mistakes for you, so
   anything you hand in has to be something you verified yourself.

## 2. The stack

| Part | Language | Where |
| --- | --- | --- |
| Control plane | Go | `services/control` |
| Node agent | Go | `services/agent` |
| Model gateway | Rust | `services/gateway` |
| AI execution | Python | `runtimes/` |
| Both web entrances | TypeScript, React 19 | `web/` |
| Business data | PostgreSQL | `services/control/migrations` |

## 3. Read these before you start

In this order. Do not skip any of them.

1. `AGENTS.md` at the repository root — **the whole file**. These are hard rules.
2. `docs/product/roadmap/index.md` — especially the tick rule and the lane table.
3. The roadmap file for your module. Your task message says which.
4. `docs/product/design/README.md` — only if your work touches a screen.
5. The `README.md` of the directory you are about to change.

## 4. Never do these

- **Never touch a path outside your lane.** Your task gives you an explicit list.
  If you see a problem elsewhere, write it in your report; do not fix it.
- **Never `git add .` or `git add -A`.** Other sessions are editing other files in
  the same checkout. Stage the exact paths you changed, listed one by one.
- **Never put a password, key or token in code, comments, tests or commit messages.**
- **Never commit** model weights, datasets, database files, dependencies or build
  output.
- **Never write Chinese** in file names, directory names, identifiers, comments,
  commit messages, error codes or log templates. English everywhere. The only
  exception is translation resources under `locales/`.
- **Never write a success stub.** A function that returns `nil` to look finished is
  worse than an unfinished item, because it hides the gap. If you cannot build
  something, record why.
- **Never force-push, rewrite history, or reset someone else's work.**
- **Never edit `docs/product/roadmap/index.md`**, and **never run
  `python3 tools/dev/roadmap_progress.py`.** That table has many writers and the
  coordinator updates it. You tick only your own line, in your own module's file.
- **Never take a migration number outside your lane's reserved block** (section 8),
  and **never edit a migration that already exists**. One that has run on a
  customer's installation cannot be changed; changing it breaks their upgrade.

## 5. The five steps, every item

### Step 1 — Your own worktree, once

Do not work in the main checkout; you will collide with other sessions.

```bash
cd "/Users/passion./Documents/Passion's WorkSpace/Development/ownsolid"
git fetch origin
git worktree add ../ownsolid-<lane> -b <lane> origin/main
cd ../ownsolid-<lane>
```

Your task message gives the lane name. Everything after this happens in that
directory.

### Step 2 — Research note before code

A hard rule from `AGENTS.md`. **Do not start writing code.** Create
`docs/research/<topic>.md` and write down:

- What problem this actually solves
- Which primary sources you read, with links: official documentation, RFCs,
  standards. Not blog posts repeating each other
- The approach you chose, **and which approaches you rejected and why**
- Every dependency you will use and its licence. This product is sold and shipped,
  so a licence that forbids that is a blocker, not a detail
- How this will fail, and how you will test those failures
- What "done" means, stated so it can be checked. Not "works correctly"

**If you cannot find reliable sources, stop and say so.** A guessed design is rework.

### Step 3 — Write it

- Comment **why**, never what. `// add one to x` is noise.
- Every value that varies by deployment — address, port, timeout, retry count,
  path, limit, credential — comes from configuration. Nothing hardcoded.
- Parse booleans explicitly. The string `"false"` must never become true.
- An error message says what happened, whether the person can fix it, and what to
  do next.

### Step 4 — Test

**Untested code is unwritten code.** All of these must pass:

```bash
cd "/Users/passion./Documents/Passion's WorkSpace/Development/ownsolid-<lane>"
make check
go build ./...
go test ./...
```

Rust lanes also run `cargo test` and `cargo clippy -- -D warnings` in their crate.

Your tests must cover the **failure** paths. A test suite that only proves the happy
case proves very little.

### Step 5 — Tick, commit, push

1. In **your module's** roadmap file, change `- [ ]` to `- [x]` on your line and
   append ` - Evidence: <commit id or test run>`. Do not touch `index.md`.
2. Commit, staging exact paths:
   ```bash
   git add <path> <path> <path>
   git commit
   ```
   The message is English, imperative, and says what you verified.
3. Push:
   ```bash
   git fetch origin && git rebase origin/main
   make check && go build ./... && go test ./...   # again, after the rebase
   git push origin HEAD:main
   ```
   If the push is rejected, rebase and retry. **Never `--force`.**

## 6. When you are stuck

Do not guess, and do not hardcode a fake result to get past it.

1. Re-read the README and the existing code near your change. The answer is usually
   already there.
2. If you are blocked by missing hardware, a missing external service or a missing
   account, that is a **boundary**: record it in your research note, leave the item
   unticked, and move to the next one.
3. If the requirement itself is unclear, **stop and ask.** Do not invent one.

## 7. How to report

After **each** item, in Chinese. Do not save them up.

- What you finished, in one sentence, in plain language — not a description of code
- Which files changed
- Which tests you ran and what they said
- What you could not do, and why
- Anything you think the owner should know: a trap you hit, something that looked
  wrong

A report that says the item is done, and nothing else, is not a report.

## 8. Database migration numbers

`services/control/migrations/postgresql/` is numbered in sequence, and two lanes
choosing the next free number will collide. Each lane therefore owns a block. Take
the lowest free number inside your block, in order, and never ask: the blocks exist
so that nobody has to coordinate.

| Lane | Block |
| --- | --- |
| A fleet core, B web console | from `000053`, in sequence |
| D command line | none needed |
| E audit | `000100`–`000119` |
| F datasets | `000120`–`000139` |
| G gateway | `000140`–`000159` |
| H offline delivery | none needed |
| M models and serving | `000160`–`000179` |

Write both files, matching the existing names:

```
000100_audit_hash_chain.up.sql
000100_audit_hash_chain.down.sql
```

Gaps between blocks are fine. The runner sorts by number and applies in order; it
does not require the numbers to be contiguous.

If your block runs out, ask for another. Do not spill into the next lane's block.
