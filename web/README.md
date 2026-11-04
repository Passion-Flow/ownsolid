# Web workspace

The console (Admin entrance) and the portal, built with React, TanStack and Tailwind CSS.
[ADR 0002](../docs/decisions/0002-frontend-stack.md) records the stack and
[the design system](../docs/development/design-system.md) is the visual standard.

| Path | Holds |
| --- | --- |
| `apps/console` | The console. Served on the admin origin |
| `apps/portal` | The portal. Served on the portal origin |
| `packages/shell` | What both applications share: startup, session, sign-in, status pages |
| `packages/ui` | Design tokens, fonts and components |
| `packages/api` | Transport to the control plane: CSRF, timeouts, problems, runtime configuration |
| `packages/i18n` | Translation runtime and the `locales/` catalogs |
| `packages/config` | ESLint, Prettier, Vite, Vitest and Playwright settings |

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

To run an application against a real control plane:

```sh
OWNSOLID_DEV_ORIGIN=https://admin.ownsolid.test:8443 \
  NODE_EXTRA_CA_CERTS=/path/to/ca.pem pnpm --filter @ownsolid/console dev
```

## Rules

- **Text:** every user-visible string is a key in `packages/i18n/locales/en.json` and
  `zh-cn.json`. ESLint rejects literal text in JSX, and a test keeps both catalogs equal
  in keys and placeholders.
- **Offline:** nothing loads from the internet. Assets are never inlined as `data:` URLs
  because the Content-Security-Policy allows fonts and scripts only from the origin.
- **Inline styles:** the same policy blocks `<style>` elements and `style` attributes in
  HTML. React's `style` prop is allowed; a library that injects a `<style>` element is not.
- **Errors:** show failures with `ProblemNotice`, which gives the cause, the next step and
  the request ID. New server error codes get `problem.<CODE>.title` and `.detail` keys.
- **File names** are kebab-case, as for all TypeScript in this repository.
