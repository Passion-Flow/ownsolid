# In-product help and API reference

What UX-111 is built on: help and API documentation that every installation serves itself, in an
air-gapped network, matching its own version.

## How others do it

- **GitLab** serves a copy of its documentation at `/help` on every self-managed instance,
  matching that instance's version; requests map onto the Markdown files of its `doc/` directory
  ([GitLab /help](https://docs.gitlab.com/development/documentation/help/)). Its own issue tracker
  records why the copy cannot be dropped: air-gapped customers depend on it, and the online site's
  search makes a web request per query, so it does not work offline
  ([issue 214164](https://gitlab.com/gitlab-org/gitlab/-/issues/214164)).
- **Harbor** serves a Swagger UI of its REST API from the instance itself, reached from the
  navigation, calling the API with the signed-in user's rights
  ([API explorer](https://goharbor.io/docs/working-with-projects/using-api-explorer/)).
- **The console's error catalog** (UX-107, [research](errors-and-empty-states.md)) already explains
  every code an API call declares, in both languages; an API reference should reuse it rather than
  write the explanations twice.

The lesson in both: the help that ships is the help for the version installed, it is reachable from
the product's own navigation, and nothing about it needs the internet, including search.

## The approach

- **Help pages at `/help` in each entrance**, written for that entrance's reader: the console's for
  people who run the installation, the portal's for people who use it. Articles are Markdown in
  `web/packages/help/locales/<language>/<entrance>/`, one file per article and language, since
  translated product text lives only in `locales/` (AGENTS.md section 7); an article about a page
  both entrances have is written once, in `shared/`. `articles.json` orders them, gives each a
  topic and names the pages whose help link opens it.
- **Parsed at build time, rendered with the design system.** `tools/codegen/web-help.mjs`, run by
  `make generate` and checked by `make check`, turns each article into an mdast tree with
  `mdast-util-from-markdown` and the GFM table extension; it refuses raw HTML, images, headings
  deeper than three levels and any link that is not a page path or `help:<article>`, so no article
  can point outside the installation. The page renders
  the tree with the design system's own components (headings, code blocks, tables, links). No HTML
  string reaches the page, so there is no sanitizer to get wrong and the Content-Security-Policy
  stays as it is. An article loads with its route, under the bundle budget.
- **Search offline.** The help index (titles, headings, a summary) is a `SearchSource` of the
  command menu and the help page's filter, matched in the browser.
- **Context.** The top bar's help button opens the article that names the longest prefix of the
  current path, else the help page; on a phone, where the top bar has no room, help is in the user
  menu. A link to a page or article that does not exist fails a test, as navigation targets do.
- **API reference at `/help/api`**, built from `api/openapi/v1` at build time and filtered to the
  entrance's audience (`x-audiences`), so the portal never lists the console's paths. Each
  operation shows its method and path, parameters, request body and response fields, and its
  declared error codes with the explanation the error catalog already gives. The contract itself
  can be saved as a file, filtered to the operations this entrance serves. It is a reference, not a
  console: the page sends nothing to the API.
- **What it leaves out, and why.** Every operation declares its entrance, and the generator fails
  on one that does not. Most inherited operations declare no `x-error-codes`; the reference says
  so on each such operation instead of guessing, until UX-131 declares them. The inherited
  knowledge contract is never listed: knowledge belongs to DeepParser (AGENTS.md
  section 1). Operation IDs repeat across contracts, so an operation's anchor is
  `<contract>.<operationId>`.

### Rejected

- **Swagger UI, Redoc or Scalar bundled.** All are permissively licensed, but each brings its own
  look, a large bundle and inline styles the installation's Content-Security-Policy refuses, and
  none knows the error catalog. The contracts are plain OpenAPI 3.1 JSON; rendering the parts a
  reader needs with the design system is smaller and consistent.
- **A runtime Markdown renderer or HTML strings.** Parsing in the browser costs bundle size for
  nothing, and HTML strings need sanitizing. The tree is made once, at build time.
- **Linking to an online documentation site.** It fails in exactly the networks this product is for.

## Licences

| Dependency | Licence | Use |
| --- | --- | --- |
| `mdast-util-from-markdown` 2.1.0 and its micromark dependencies | MIT | build time only |
| `mdast-util-gfm-table` 2.0.0, `micromark-extension-gfm-table` 2.1.2 | MIT | build time only |
| `@types/mdast` | MIT | types |

Nothing is downloaded at runtime; the installation serves the built files. `mdast-util-gfm-table`
2.0.1 was four days old at the time of choosing, so 2.0.0 is pinned.

## Failure modes and tests

- An article missing in one language, or a link to a page or article that does not exist: a unit
  test fails the build's tests.
- An operation without `x-audiences`, or an audience leaking into the other entrance: a unit test
  over the generated reference.
- A code with no explanation: already enforced by the error catalog's coverage test.
- Search with no match says so and offers to clear it; both languages; axe in both themes;
  keyboard; the bundle budget.
- Offline: the live run checks that no request leaves the installation.

## What the finished item does

Both entrances have `/help` with articles for their reader, searchable from the page and the command
menu, a help link on each page that has an article, and `/help/api` listing every operation that
entrance serves with its fields and explained errors, in both languages, with nothing fetched from
outside the installation.
