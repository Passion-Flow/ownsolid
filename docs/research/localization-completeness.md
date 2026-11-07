# English and Simplified Chinese, complete

Research behind UX-108: every page, email and error in both languages.

## What was already true, and what was not

Measured on `main` on 2026-11-07, before this item:

| Surface | Finding |
| --- | --- |
| Catalogs | 1,448 keys. Chinese has every key and placeholder, enforced since UX-001 |
| JSX | ESLint rejects literals, so pages held no hand-written English in JSX |
| Errors | All 75 declared codes explained. `PublicProblem` is the one exit for API failures: 807 public codes exist, and anything else leaves as `STORAGE_UNAVAILABLE` |
| Emails | Three purposes, both languages, with `Content-Language` and the account's language |
| Audit trail | Actions and targets shown as raw identifiers in both languages |
| Run and task failures | A raw code in the notifications and the portal's home |
| API reference | Field descriptions English only, some in inherited project jargon; contract names as identifiers |
| Hand-joined text | `${label}: ${value}` with Latin punctuation in Chinese; `cmdk`'s default label "Suggestions" |
| Terms | Two words for "account", two forms of "you" |

The gap was not untranslated keys. It was text that never passed through a key, which no
catalog test can see.

## How others solve it

- **Audit events.** Okta's System Log carries a stable `eventType` and a human `displayMessage`
  on each [LogEvent](https://developer.okta.com/docs/api/openapi/okta-management/management/tag/SystemLog/),
  and [filters on the type](https://okta.com/integrate/documentation/isv-syslog-references). The
  identifier is the contract and the sentence is for people. We keep both, and the sentence comes
  from the catalog, so it follows the reader's language rather than the writer's.
- **Finding text that bypasses the catalog.**
  [Android's pseudolocales](https://developer.android.com/guide/topics/resources/pseudolocales)
  make hard-coded strings stand out because they alone stay unchanged. The principle is to test
  at the translation boundary. We apply it without a third language: a string on a page must be a
  catalog message or a value from the server, so the existing page states, written against
  English, need no second set of selectors.
- **Spacing between Chinese and Latin text.** The
  [W3C Chinese layout requirements](https://www.w3.org/TR/2015/WD-clreq-20150723) discuss the gap
  without one fixed value, and CSS `text-autospace` is not yet consistent across browsers
  ([issue](https://lists.w3.org/Archives/Public/public-i18n-archive/2024JulSep/0406.html)). The
  catalog already writes a plain space around Latin words, and keeps doing so.
- **Browser validation bubbles** follow the browser, not the product, so every form sets
  `noValidate` and shows its own message.

## Choices

- **Audit: a name per action, by family.** A family with a page has a name for each action, and a
  test reads the server's source to hold it there. Other families (training, datasets and the
  like) show the family's name and the identifier until their page ships and joins the list. The
  identifier stays in the details, as a filter and support reference. Knowledge actions are
  deliberately unnamed: knowledge is a partner's product (AGENTS.md section 1).
- **Errors: what a page can meet, not all 807 codes.** The coverage test follows the API calls the
  web client makes to their declared codes. A new call fails until both languages explain it. The
  remaining codes belong to pages that do not exist, and writing them now would be writing them
  blind. An undeclared code shows the general explanation with its request ID, in both languages.
  UX-131 declares the rest.
- **The API speaks English.** `title` and `detail` of a problem stay English for scripts; the web
  client explains by `code`. A stable code is the contract.
- **Contracts stay English; translations sit beside them.** `help/locales/zh-cn/api-descriptions.json`
  is keyed by the English text, and the generator fails on a missing or an unused entry.
- **Left as recorded.** Task log lines and an audit event's reason are records: logs come from the
  node, and a reason is typed by a person or written by the server at the time. Translating them
  would alter evidence. Names people give things are data.
- **Rejected: a pseudo-locale build.** It would need every page state rewritten against
  transformed selectors, for the same answer.
- **Rejected: translating on the server.** It would make stored audit rows depend on who wrote them.

## Glossary

One word for one thing across the catalog, help and emails, kept beside the catalogs in
[`web/packages/i18n/locales/glossary-zh-cn.md`](../../web/packages/i18n/locales/glossary-zh-cn.md).
The catalog test holds its first five rows.

## Licences

No new dependency, model or data set.

## Failure modes and tests

- Text that bypasses the catalog: `localizationFlows` walks every page state of both entrances.
- A catalog key missing in Chinese: the catalog test, unchanged.
- A new audit action in a family with a page: `audit-coverage.test.ts`.
- A contract description changed without its translation: `make generate` fails.
- An email in the wrong language: `TestEveryMailPurposeReadsInEachLanguage`, then a live delivery.
- A term outside the glossary, or Latin punctuation beside Chinese: `catalogs.test.ts`.

The walk cannot judge a translation's quality, and it passes a value the server sends as data.
A bare enum value does not count as data, which is what catches a raw status.

## What the finished item must do

A person reading Chinese meets no English on any page except names, identifiers shown as such,
and recorded text. Every email arrives in the account's language, and the checks above fail when
that stops being true.
