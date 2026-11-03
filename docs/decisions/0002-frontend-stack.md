# 0002: Frontend stack and design language

- Status: accepted
- Date: 2026-11-04

## Context

The previous frontend was about 3,200 lines of TypeScript spread over 29 packages,
with no component library, no lint and navigation built from nested profile checks.
The product needs a standard, professional interface for data-dense administration
that is fast to use, and it must work fully offline.

## Decision

- React 19, TypeScript and Vite, as before.
- TanStack Router and TanStack Query for routing and server state; TanStack Table for tables.
- shadcn/ui on Radix primitives with Tailwind CSS 4. Component source lives in
  `web/packages/ui`, so nothing is fetched at runtime and the visual language is ours.
- react-hook-form with zod for forms, ECharts for charts, Monaco for code and logs,
  `@xyflow/react` for the workflow canvas.
- ESLint and Prettier for style; Vitest and Playwright for tests.
- Two applications, `console` (Admin) and `portal`, share `ui`, `api`, `i18n` and
  `config` packages.

### Visual language

- **Palette:** warm neutral surfaces with a single teal accent (`#0d9488` light,
  `#14a394` dark). Status colors are reserved for state and always come with a label.
- **Type:** Figtree for the interface and JetBrains Mono for code and logs, both
  bundled (SIL OFL 1.1). Chinese text uses the system font stack, so offline bundles
  do not carry large CJK font files.
- **Icons:** Lucide (ISC), bundled locally, 24-pixel grid with a 1.75 stroke. Use
  literal, professional metaphors (server, database, key); avoid playful ones such
  as rockets, magic wands or puzzle pieces.
- Light and dark themes; comfortable and compact density.

The interaction principles live in [the design system guide](../development/design-system.md).

Ant Design was considered. It is faster for dense forms but its look is widely
recognizable and harder to make distinctive.

## Consequences

- Every page uses shared patterns: filter bar, table, detail drawer; long tasks show
  a status timeline and live logs.
- Visual tokens are defined once in `web/packages/ui` and reused everywhere.
- Font and icon licenses ship with the product's third-party notices.
