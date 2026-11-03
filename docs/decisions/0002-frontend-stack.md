# 0002: Frontend stack

- Status: accepted
- Date: 2026-11-04

## Context

The previous frontend was about 3,200 lines of TypeScript spread over 29 packages,
with no component library, no lint and navigation built from nested profile checks.
The product needs a premium, standard interface for data-dense administration, and
it must work fully offline.

## Decision

- React 19, TypeScript and Vite, as before.
- TanStack Router and TanStack Query for routing and server state; TanStack Table for tables.
- shadcn/ui on Radix primitives with Tailwind CSS 4. Component source lives in
  `web/packages/ui`, so nothing is fetched at runtime and the visual language is ours.
- react-hook-form with zod for forms, ECharts for charts, Monaco for code and logs,
  `@xyflow/react` for the workflow canvas, Lucide icons bundled locally.
- ESLint and Prettier for style; Vitest and Playwright for tests.
- Two applications, `console` (Admin) and `portal`, share `ui`, `api`, `i18n` and
  `config` packages.

Ant Design was considered. It is faster for dense forms but its look is widely
recognizable and harder to make distinctive.

## Design language

Calm and precise, drawing on Claude and Apple: warm neutral surfaces, one restrained
accent color, strong type hierarchy, an 8-point grid, 10 to 12 pixel radii, soft
depth and restrained motion. Light and dark themes, comfortable and compact density.
Fonts come from the system stack and bundled files only, never from the network.

## Consequences

- Every page uses shared patterns: filter bar, table, detail drawer; long tasks show
  a status timeline and live logs.
- Visual tokens are defined once in `web/packages/ui` and reused everywhere.
