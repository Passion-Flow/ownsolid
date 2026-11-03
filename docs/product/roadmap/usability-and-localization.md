# Usability and localization

The new console and portal, built to the design system, and the localization pipeline.

- Owns: `web/`, `docs/development/design-system.md`.
- Depends on: architecture-and-scale for API shapes; every module for its pages.

## P0 Foundation

- [ ] UX-001 Web workspace: `web/apps/console`, `web/apps/portal`, `web/packages/{ui,api,i18n,config}`, ESLint, Prettier, Vitest, Playwright (P0)
- [ ] UX-002 Design system package: tokens (warm neutrals, teal accent), Figtree and JetBrains Mono bundled, Lucide icons, light and dark, density (P0)
- [ ] UX-003 Shell: grouped navigation, workspace switcher, command menu (Cmd+K), notifications, user menu (P0) - Catalog: F427
- [ ] UX-004 Typed API client generated from `api/openapi` (P0) - Catalog: F540
- [ ] UX-005 Remove the old `apps/web` code after the new console reaches parity for migrated pages (P0)

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

## P2 Fleet operations

- [ ] UX-201 Wall display and NOC dashboards for operations centers (P2)
- [ ] UX-202 Branding: logo, product name and theme per installation (P2) - Catalog: F060, F061
- [ ] UX-203 Translation pipeline: string extraction, glossary, placeholder checks, reviewed language packs (P2) - Catalog: F546 to F551, F554, F556 to F564

## P4 Breadth

- [ ] UX-401 Remaining languages from the 249-language target, with RTL and fonts (P4) - Catalog: F546
- [ ] UX-402 Mobile companion for alerts and approvals (P4)

Catalog rows absorbed: F001 to F005, F060, F061, F236, F424 to F431, F435, F536 to F565, F566, F569.
Engineering rules F536 to F545 also live in AGENTS.md.
