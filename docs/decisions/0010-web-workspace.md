# 0010: Web workspace conventions

- Status: accepted
- Date: 2026-11-04

## Context

ADR 0002 chose the stack. Building `web/` raised choices it left open, and a few pins
differ from what the newest releases suggest.

## Decision

- **A fifth package, `packages/shell`,** holds what the console and portal share: startup,
  session, sign-in and status pages. `ui` stays free of API and router knowledge.
- **Routes are written in code,** not generated from files, so no generated route tree has
  to live outside a `gen/` directory. Shared routes are defined in `shell`; each application
  adds its pages under `authenticatedRoute`.
- **Runtime configuration loads in the root route,** before any other request, as a query.
  A failure there is an ordinary route error with a retry.
- **Translations use a small typed runtime** on `Intl`, not i18next. Keys are checked by the
  compiler against `en.json`, and non-English catalogs load on demand.
- **ESLint rejects literal text in JSX** with `no-restricted-syntax`, which keeps the
  English and Chinese coverage complete without another plugin.
- **The Content-Security-Policy stays strict.** `style-src 'self'` blocks `<style>` elements
  injected at runtime, including the scroll lock in Radix's modal dialog. Dialogs use the
  native `<dialog>` element and menus run non-modal, rather than loosening the policy.
- **Browser tests run with the production policy.** `vite preview` sends the same header,
  and a unit test keeps that copy equal to the control plane's.
- **Pins:** TypeScript 6.0, because typescript-eslint supports nothing newer yet. ESLint 10,
  because 9 is deprecated, with the maintained `eslint-plugin-jsx-a11y-x` fork, because
  `eslint-plugin-jsx-a11y` has no release for it. pnpm's default minimum release age stays
  on, so a dependency is at least a day old when it is adopted.
- **One generated module and one client per contract,** not a merged `paths` type: six
  paths appear in both the `components` and `nodes` contracts. Generated types are committed
  under `packages/api/src/gen`, and `make check` fails when they differ from `api/openapi`.
  `openapi-typescript` asks for TypeScript 5; a peer rule allows 6 because the drift check and
  the compiler verify its output. The client covers JSON endpoints; file transfers and event
  streams use their own code when their pages arrive.
- **The control image builds `web/` itself,** from `pnpm-lock.yaml`, with install scripts
  disabled. Only building an image needs a registry; a running installation never does.

## Consequences

- Moving to TypeScript 7 waits for typescript-eslint support.
- A library that injects `<style>` at runtime cannot be used without replacing that part.
- `make check`, `make build` and `make test` now need Node.js, pnpm and, for the browser
  tests, Chromium.
