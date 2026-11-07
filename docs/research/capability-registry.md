# Capability registry

What CMP-215 and the page S-106 are built on: one declaration of every option the product
offers, each with a state, a reason and the roadmap item behind it.
[ADR 0036](../decisions/0036-show-every-option-and-fetch-models-offline.md) decided the four
states and how an unavailable option is presented;
[ADR 0042](../decisions/0042-capability-declaration-follows-the-roadmap.md) records what this
note chose.

## How others do it

- **Kubernetes** keeps feature gates with their stage in its own source tree. The reference pages
  on the website are regenerated from that data: a
  [website pull request](https://github.com/kubernetes/website/pull/53847) syncs the pages from a
  `featuregates.json`, and its review says a wrong value is fixed in the Kubernetes repository, so
  that the generated page comes out right. [Configuring feature gates](https://kubernetes.io/docs/tasks/administer-cluster/configure-feature-gates/)
  points to that reference for what each gate affects.
- **Grafana** defines each feature toggle and its stage in one registry file; generation writes the
  backend constants, the frontend types and a documentation page marked as machine generated
  (`contribute/feature-toggles.md` in the Grafana repository, read on a
  [mirror](https://forger.sitiv.fr/apitech/grafana/raw/tag/v11.5.6+security-01/contribute/feature-toggles.md)).
- **GPUStack**, the closest product, publishes accelerators, engines and node systems as prose in its
  [package description](https://pypi.org/project/gpustack) and installation pages. Its lists changed between
  releases and disagree across pages, which is the failure a registry exists to prevent.
- **USWDS and ARIA** on the presentation of an unavailable option: recorded in ADR 0036.

The pattern that holds: one data source inside the product's repository, stages in that source,
and every reader derived from it. Nothing in these products ties a stage to a project plan; here the
tie is the point, because the roadmap's tick is what a claim of support means (AGENTS.md section 4).

## Licences

No new dependency. The declaration is a JSON file read with the Go standard library, and the page
uses what the console already ships. The names in it are descriptive use of product names.

## The approach

- **`pkg/capabilities/declaration.json`, embedded with `go:embed`.** It is a shared Go library
  (AGENTS.md section 6) because two programs in different modules need it without the network or the
  database: the installer and the control plane. The English text lives there, since the installer and
  API clients have no catalog.
- **The API serves the embedded document** at `GET /api/v1/capabilities` to any signed-in person in
  either entrance. It holds no tenant data and does not change while a process runs, so it has no paging or
  filters: there are a few dozen rows, and the page filters them in the browser.
- **The installer and the control plane read their topology sizes from it.** A size that is not ready
  is refused with the declared reason and item. `ownsolid-install capabilities` prints the same table
  with no configuration, so an operator can ask before a file exists.
- **Translated text sits in the catalogs** under `capability.<registry>.<option>.*`. A test holds the
  English catalog equal to the declaration and every other catalog to the same keys.
- **A row follows the roadmap, by test.** `ready` and `licensed` name an item that is ticked, `planned`
  one that is open, and `unsupported` names none. Ticking an item without moving its row fails
  `make test`, in whichever lane does it.

### Rejected

- **A TypeScript file like the demo's.** The installer and the control plane could not read it.
- **Generated Go and TypeScript constants.** Grafana needs them because code branches on each toggle.
  Here code branches only on topology size, and a test holds those constants to the declaration.
- **Text per locale in the API.** It would move translations out of `locales/` and into a Go module.
- **A state per license or per installation.** The declaration says what this release can do. What one
  installation has turned on belongs to that installation's own pages.
- **Linking a roadmap item to a public page.** The plan's S-106 asks for it, but no public roadmap exists,
  the repository is private and an air-gapped console could not open one. The page shows the ID with a copy
  button, and the reason says what is missing. UX-111 can link it once help ships inside the product.

## Failure modes and tests

| Failure | Test |
| --- | --- |
| A row claims `ready` while its item is open, or stays `planned` after the tick | `TestEveryRowAgreesWithTheRoadmap` |
| A row names an item that does not exist | same test |
| A non-ready row has no reason; a duplicate ID; an unknown state or key | `TestParseRefuses` |
| The catalogs fall behind the declaration | `capability-coverage.test.ts` |
| A size the installer accepts differs from the declaration | `TestTopologySizesAreDeclared`, installer tests |
| The API refuses or fails | the page's error state with retry, and a browser flow |
| A person who is not signed in reads the document | `GET` without a session answers 401 in the live flow |
| A failed read in the add-node dialog | the line goes away; adding a node does not depend on it |

## What the finished item does

- Every registry of S-106, plus install sizes, business databases and single sign-on providers, is
  declared with state, reason and item.
- Console and portal both have the page at `/support`: one table per registry with option, state, reason and
  item; tiles and a search that live in the address; loading, filtered-empty and error states; both languages.
- `ownsolid-install capabilities` prints it, and a refused size quotes it.
- The add-node dialog names the systems that can join, from the same declaration.

## Open questions

- CMP-216 builds the control for an unavailable option once a form offers one; no form in the console offers a
  registry's choices yet, so the first model form takes it from the same API.
