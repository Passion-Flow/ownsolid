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

- **Never touch a path outside your lane**, except the shared files listed in
  section 9, which every lane is pre-authorised to add to. If you see a problem
  elsewhere, write it in your report; do not fix it.
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

## 6. When you are stuck, decide and keep going

**You never stop and wait.** The owner may be away for a day, and a lane that waits
has stopped for that day. There is no question worth that.

When you reach something you would have asked about:

1. Decide it yourself with the defaults in section 11.
2. Write the decision in `docs/research/<lane>-decisions.md`: what you decided, why,
   what you considered and rejected, and how much damage it does if you were wrong.
3. Carry on.

If there is genuinely no safe option: leave the item unticked, record it in the same
file as a boundary saying what blocks it and what you need from the owner, and
**move to the next item in your module**.

Before deciding, re-read the README and the code around your change. The answer is
usually already there, and a decision taken from the code beats one taken from a
default.

## 7. How to report

After **each** item, in Chinese. Do not save them up, and **do not wait for a
reply**: write the report, then start the next unticked P1 item in your module.
Work down the list until the owner tells you to stop. If an item's dependencies are
not met, skip it and take the next one.

- What you finished, in one sentence, in plain language — not a description of code
- Which files changed
- Which tests you ran and what they said
- What you could not do, and why
- Anything you think the owner should know: a trap you hit, something that looked
  wrong

A report that says the item is done, and nothing else, is not a report.

## 8. Database migration numbers

`services/control/migrations/postgresql/` is **one contiguous lineage starting at
1**. This is enforced in `services/control/internal/platform/database/database.go`:
a file whose number is not exactly one more than the previous one fails with
`MIGRATION_SEQUENCE_INVALID`. Reserved blocks and gaps are therefore impossible.

Two constants in that same file move with every migration, and you update both:

- `SchemaVersion` — the number of migrations. Add one.
- `TableCount` — the number of public tables. Add however many your migration
  creates.

Because the numbering is a single line, two lanes will sometimes want the same
number. Resolve it at push time, not by asking:

1. While you work, use the next number after the highest on `origin/main`.
2. Before pushing: `git fetch origin && git rebase origin/main`.
3. If another lane took your number while you were working, rename your two files
   to the next free number, correct `SchemaVersion` and `TableCount`, and rerun the
   tests.
4. Push. If the push is rejected, go back to step 2.

Git serialises pushes, so whoever lands first keeps the number and the other
renames. No coordination is needed and nobody has to be asked.

**Never renumber or edit a migration that already exists** to close a gap or make
room. One that has run on a customer's installation cannot change.

## 9. Shared files you may add to, without asking

Some work cannot land without touching a file every lane shares. Asking each time
makes the owner a bottleneck for a decision that is the same every time, so these
are pre-authorised. **One rule covers all of them: add your own entry, change
nothing else.**

| File | What you may do |
| --- | --- |
| `go.work` | Add one `use` line for a module you created |
| A test that enumerates schema objects, such as the backup or restore reference test | Add the tables your migration adds |
| `services/control/migrations/postgresql/` | Create the next contiguous migration (section 8) |
| `SchemaVersion` and `TableCount` in `platform/database/database.go` | Move both to match your migration |
| Your own module's roadmap file | Tick your line and add its evidence |
| `docs/research/` | Your own notes |

**Never reorder, reformat, or delete anyone else's entry in these files**, and never
edit a migration that already exists.

When a rebase conflicts in one of them, the resolution is always the same: keep
everything that is already there, then re-apply your own line. If you find yourself
wanting to remove someone else's line to make a conflict go away, stop and ask.

### When to stop and ask anyway

Pre-authorisation covers adding. It does not cover these, and for them you still
stop:

- Changing or deleting something another lane wrote
- Changing a shared contract: an API schema, a protocol version, a published
  interface that other modules compile against
- Anything that would make another lane's tests fail
- Adding a third-party dependency whose licence you could not verify

## 10. Never block. Write the question down and keep going

The owner is not always at the keyboard, and a lane that stops to ask has stopped
for the night. **A question must never halt your lane.**

When you reach something section 9 says to ask about:

1. Append it to `docs/research/<your-lane>-questions.md`: what you need decided,
   which options you see, which one you would pick, and what it blocks.
2. **Take the safest option that does not need the answer**, and say in the file
   that you did.
3. If nothing safe exists, leave the item unticked, record it as a boundary, and
   **move to the next item in your module.**
4. Keep working.

The safest option is almost always the one that changes the least of other
people's work: add rather than modify, keep the existing behaviour and put yours
behind a new path, leave the old code in place.

What this does not license: it is not permission to guess at a design, to write a
stub that pretends to work, or to change a shared contract because waiting was
inconvenient. Those three still stop, and stopping means moving to the next item,
never inventing an answer.

Put every question in that one file so the owner can read them all at once, rather
than finding them scattered through a transcript.

## 11. Defaults, so nothing has to be asked

| Situation | What you do |
| --- | --- |
| Migration number | Highest on `origin/main` plus one; move `SchemaVersion` and `TableCount` with it. If another lane lands first, rebase, rename, rerun, push again |
| A shared file from section 9 | Add your own entry. Leave every other line alone |
| The requirement is unclear | Take the reading that changes the least of other people's work, and record it |
| You would have to change a shared contract | Do not. Go around it additively: keep the existing behaviour, put yours behind a new path or a new field. If that is impossible, record a boundary and skip the item |
| A dependency's licence cannot be verified | Do not use it. Find one you can verify, or write it yourself |
| Missing hardware, service or account | Record a boundary, leave the item unticked, take the next one |
| The environment a test needs will not start | Write the test, mark that it needs that environment, record the boundary, take the next item |

### No default, and no exception

These have no "ask the owner" path either. They are simply not done:

- A stub that returns success to look finished
- A design guessed because the sources could not be found
- Editing or deleting another lane's work
- `--force`, rebasing away someone's commits, rewriting history
- A credential in the repository
- Anything that reaches the network at runtime
