# Web workspace

The console (Admin entrance) and the portal, built with React, TanStack and Tailwind CSS.
[ADR 0002](../docs/decisions/0002-frontend-stack.md) records the stack and
[the design system](../docs/development/design-system.md) is the visual standard.

| Path              | Holds                                                                                         |
| ----------------- | --------------------------------------------------------------------------------------------- |
| `apps/console`    | The console. Served on the admin origin                                                       |
| `apps/portal`     | The portal. Served on the portal origin                                                       |
| `packages/shell`  | What both applications share: startup, session, sign-in, status pages and the signed-in frame |
| `packages/ui`     | Design tokens, fonts and components                                                           |
| `packages/api`    | Transport to the control plane: CSRF, timeouts, problems, runtime configuration               |
| `packages/i18n`   | Translation runtime and the `locales/` catalogs                                               |
| `packages/help`   | Help articles of both entrances in `locales/`, and the API reference generated from contracts |
| `packages/config` | ESLint, Prettier, Vite, Vitest and Playwright settings                                        |

## Working here

Node.js and pnpm versions are in [the environment guide](../docs/development/environment.md).
Dependency versions live only in the `catalog:` of `pnpm-workspace.yaml`. Dependencies
never run install scripts.

```sh
pnpm install --frozen-lockfile --ignore-scripts
pnpm lint && pnpm format:check && pnpm typecheck
pnpm test            # Vitest, every package
pnpm test:browser    # Playwright: builds each application and runs its e2e/ specs
pnpm build           # apps/*/dist, which the control image serves
```

Browser tests need Chromium once per machine:
`cd apps/console && pnpm exec playwright install chromium`. They run against a scripted
control plane (`packages/shell/testing`) and `vite preview` with the control plane's
Content-Security-Policy, so anything production would block fails the test.
When worktrees run browser tests concurrently, set `OWNSOLID_PLAYWRIGHT_PORT_OFFSET`
to a different integer for each worktree. It shifts both preview ports (4310 and 4311)
by that amount and works with `make test`.

To run an application against a real control plane:

```sh
OWNSOLID_DEV_ORIGIN=https://admin.ownsolid.test:8443 \
  NODE_EXTRA_CA_CERTS=/path/to/ca.pem pnpm --filter @ownsolid/console dev
```

## Writing help

An article is a Markdown file in `packages/help/locales/en/<entrance>/` and the same name under
`zh-cn/`; an article both entrances need goes in `shared/`. List it in
`packages/help/articles.json` with its topic and the pages whose help link opens it, then run
`make generate`. It starts with one `#` title and a summary paragraph; links go to a page path such
as `/nodes` or to another article as `help:<slug>`. The generator refuses anything else, and the
tests fail when a language is missing or a link has no page.

Contract download addresses are bundled as URL strings. The JSON files remain separate
same-origin assets and are read only when someone saves one. Finding a contract in the
reference must not depend on another request for its address.

## Calling the API

`packages/api/src/gen` holds types generated from `api/openapi`; change a contract, then run
`make generate` and commit the result. `make check` fails until they match. Call an endpoint
through a client for its contract, so the compiler checks the path, parameters and body:

```ts
const nodes = createClient<nodesContract.paths>();
const page = await nodes.get('/api/v1/sites/{site_id}/cells/{cell_id}/admin/nodes', {
  path: { site_id: 'default', cell_id: 'default' },
  query: { limit: 20 },
});
```

Writes carry the CSRF token and retry once when it is stale. Where the contract requires
`Idempotency-Key`, `idempotencyKey` is required: create it when the user starts the action
and reuse it for retries.

## Rules

- **Text:** every user-visible string is a key in `packages/i18n/locales/en.json` and
  `zh-cn.json`. ESLint rejects literal text in JSX, and a test keeps both catalogs equal
  in keys and placeholders.
- **Offline:** nothing loads from the internet. Assets are never inlined as `data:` URLs
  because the Content-Security-Policy allows fonts and scripts only from the origin.
- **Inline styles:** the same policy blocks `<style>` elements and `style` attributes in
  HTML. React's `style` prop is allowed; a library that injects a `<style>` element is not.
- **Pages load with their route:** a route's component comes from `lazyRouteComponent`, and
  a large library is imported only by the page that needs it. Workspace packages declare
  `sideEffects`, so an index import does not pull every module into the first download.
  Each build checks the bundle budget in `packages/config/src/vite.ts`
  ([ADR 0016](../docs/decisions/0016-route-level-code-splitting.md)) and fails when the
  first download or a later chunk is over it.
- **Accessibility:** every page state is listed in the application's `e2e/pages.ts`. The
  browser tests run axe-core on each in both themes against WCAG 2.2 AA and walk each
  with Tab, failing on a control that cannot be reached or does not show focus. A route
  without a state fails the application's unit tests.
- **Errors:** show failures with `ProblemNotice`, which gives the cause, the next step and
  the request ID. New server error codes get `problem.<CODE>.title` and `.detail` keys.
- **File names** are kebab-case, as for all TypeScript in this repository.
