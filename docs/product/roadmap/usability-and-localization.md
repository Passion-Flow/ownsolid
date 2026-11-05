# Usability and localization

The new console and portal, built to the design system, and the localization pipeline.

Reviewed 2026-11-04 against current React, TanStack, Vite, Tailwind CSS and Playwright
releases and comparable consoles. The stack in ADR 0002 stands; the pins that differ from
the newest releases are in ADR 0010. The review added UX-113 to UX-120.

- Owns: `web/`, `docs/development/design-system.md`.
- Depends on: architecture-and-scale for API shapes; every module for its pages.

## P0 Foundation

- [x] UX-001 Web workspace: `web/apps/console`, `web/apps/portal`, `web/packages/{ui,api,i18n,config}`, ESLint, Prettier, Vitest, Playwright (P0) - Evidence: ed470b5, 799ed29; zero-egress install and real-browser sign-in on `ownsolid-cpu`, 2026-11-04
  - Also build `web/` into `deploy/images/control` in place of the placeholder page. DEL-001 waits for this.
- [x] UX-002 Design system package: tokens (warm neutrals, teal accent), Figtree and JetBrains Mono bundled, Lucide icons, light and dark, density (P0) - Evidence: d12d0ff; real-browser run on `ownsolid-cpu`, 2026-11-04
- [x] UX-003 Shell: grouped navigation, workspace switcher, command menu (Cmd+K), notifications, user menu (P0) - Catalog: F427 - Evidence: ff9561a; real-browser run on `ownsolid-cpu`, 2026-11-04
- [x] UX-004 Typed API client generated from `api/openapi` (P0) - Catalog: F540 - Evidence: 2bfea74; real-browser run on `ownsolid-cpu`, 2026-11-04
- [x] UX-005 Remove the old `apps/web` code after the new console reaches parity for migrated pages (P0) - Evidence: 3eed593; real-browser run and zero-egress install on `ownsolid-cpu`, 2026-11-04
  - `apps/web` lived in the archived `project-one` repository and never entered this one, so this item removes what still pointed at it. The pages that emails and sign-in providers link to moved here: registration by invitation, password reset and the single sign-on return. Every other old page moves with its module's P1 item.

## P1 Enterprise release

- [ ] UX-101 Console overview: quick actions, needs-attention list with next steps, setup checklist, fleet health (P1) - Catalog: F435
- [ ] UX-102 Portal home: apps and assistants, conversations, my resources and usage (P1) - Catalog: F425, F426, F429
- [ ] UX-103 Shared list pattern: filter bar, saved views, column chooser, bulk selection with action bar, detail drawer (P1)
- [ ] UX-104 Long-task pattern: step timeline, live log, tasks continue when the page is left (P1)
- [ ] UX-105 Undo for reversible actions; typed confirmation only for irreversible ones (P1)
- [ ] UX-106 Global search across nodes, models, apps, users and tasks (P1)
- [ ] UX-107 Empty states and errors that state the cause and the next action (P1) - Catalog: F022
- [ ] UX-108 English and Simplified Chinese complete for every page, email and error (P1) - Catalog: F537, F552
- [ ] UX-109 Language preference per user, tenant default and browser negotiation (P1) - Catalog: F553
- [ ] UX-110 Locale formatting for dates, numbers and units (P1) - Catalog: F555
- [ ] UX-111 In-product help and API documentation available offline (P1) - Catalog: F431
- [ ] UX-112 Keyboard navigation, focus order and screen-reader labels on all pages (P1) - Catalog: F569
- [ ] UX-113 Warn before a session ends from inactivity, with one action to stay signed in (P1)
- [ ] UX-114 Shareable links: filters, sort order and the open drawer live in the URL (P1)
- [ ] UX-115 Route-level code splitting and a bundle size budget enforced in CI (P1)
- [ ] UX-116 Automated accessibility checks in the browser tests of every page (P1)
- [ ] UX-117 Replace the inherited browser suite in `services/control/internal/testing/browser` with Playwright flows as pages land; its `tests/acceptance` scripts did not move here, so it cannot run (P1)
- [ ] UX-118 Theme and density follow the user across browsers through `/api/v1/preferences` (P1)
- [ ] UX-119 Account page: profile, password change and linked sign-in providers (P1)
- [ ] UX-120 Mail delivery status for administrators, so a failed invitation or reset email can be seen and resent (P1)
- [ ] UX-121 Precompressed web assets: the build writes gzip and Brotli copies and the control plane serves them by `Accept-Encoding` (P1)

## P2 Fleet operations

- [ ] UX-201 Wall display and NOC dashboards for operations centers (P2)
- [ ] UX-202 Branding: logo, product name and theme per installation (P2) - Catalog: F060, F061
- [ ] UX-203 Translation pipeline: string extraction, glossary, placeholder checks, reviewed language packs (P2) - Catalog: F546 to F551, F554, F556 to F564

## P4 Breadth

- [ ] UX-401 Remaining languages from the 249-language target, with RTL and fonts (P4) - Catalog: F546
- [ ] UX-402 Mobile companion for alerts and approvals (P4)

Catalog rows absorbed: F001 to F005, F060, F061, F236, F424 to F431, F435, F536 to F565, F566, F569.
Engineering rules F536 to F545 also live in AGENTS.md.
