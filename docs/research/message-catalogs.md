# Message catalogs in the bundle

Research behind UX-129: the first download holds the whole English catalog, 19 kB of gzip that grows with every
page, so the 160 kB limit was reached and raised to 170 kB for UX-123.

## What the numbers say

Measured on the console's build at UX-127, with the bundle budget's own reader:

| | Gzip |
| --- | --- |
| First download | 160.8 kB |
| English catalog | 19 kB |
| Simplified Chinese catalog, already its own chunk | 20 kB |

A person reading Chinese, who is this product's first reader, downloads both catalogs and uses one.

## Choices

- **English becomes a chunk like every other language.** Nobody downloads a language they do not read, the first
  download falls, and a change of wording no longer changes the address of the application's code, so an upgrade
  that only edits text costs one small file. The limit comes back down, below where it started, to 150 kB.
- **What it costs:** an English page now waits for one more request before it can draw, which a Chinese page already
  did. The chunk is precompressed like the rest (UX-121) and cached by its hashed name, so this is once per release.
- **Every catalog has every key, so the translator stops falling back to English.** The catalog tests have enforced
  this since UX-001, and the fallback would otherwise have kept English in the first download for a case that does
  not ship. A key the code made up shows as itself.
- **If a language cannot load, English is tried. If English cannot load, the page says so in English from the code**
  and offers a reload, the one message that lives there, because nothing else can say it. It is the same situation the
  router's reload for a stale deployment handles (ADR 0016): a release replaced the files under an open page.

Rejected for now: splitting each catalog by page, which is what the roadmap first said. It would save a further part
of 19 kB on a first visit at the cost of a key-to-page map to maintain and a request per page, and every page shares
much of its text with the frame. It is kept as UX-130, for when a catalog is large enough to be worth that.

## Licences

No new dependency.

## Failure modes and tests

- A language chunk that will not load falls back to English; browser flow.
- No catalog at all says so and a reload recovers; browser flow.
- A catalog missing a key cannot ship; the catalog test, unchanged.
- The first download stays under its limit; the build fails otherwise.
- A Content-Security-Policy on the real install allows the chunk, and a real page draws; live run.

## What the finished item must do

An English visitor and a Chinese visitor each download only their own catalog, and the build enforces the lower limit.
