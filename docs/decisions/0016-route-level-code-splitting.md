# 0016: Route-level code splitting and a bundle budget

Status: accepted (UX-115).

## Context

Each application built into one 538 kB script (173 kB gzip): the sign-in page downloaded
the signed-in frame, the command menu and every dialog and menu library. P1 adds dozens of
pages, some with heavy dependencies such as charts and editors, so without a rule the
first download grows with every page.

## Decision

- **Pages load with their route.** A route's component comes from `lazyRouteComponent`.
  When a route renders a lazy part inside its own wrapper, `preloading` gives the wrapper
  that part's preload, so the router fetches the code while the route loads and when a
  link to it is hovered. The signed-in frame is a lazy layout route, `createFrameRoute`.
- **Workspace packages declare their side effects** (`"sideEffects": false`, CSS only for
  `ui`). Without it, the bundler kept every module behind a package's index in the first
  download, because importing an index could have run them.
- **A stale deployment reloads once.** After an upgrade, an open page may ask for a file
  the new build no longer has. The router reloads the document once for a missing module;
  if the file is still missing, the route's error page offers a retry.
- **A budget fails the build.** `bundleBudgetPlugin` measures gzip bytes in every
  production build, which `make build` and CI run: the first download (the entry, its
  static imports and their CSS) may be at most 160 kB, and any later chunk at most 60 kB.
  When it was set the first download was 138 kB and the largest chunk, the frame, 39 kB.
  Fonts and images are not counted. Raising a limit is a change to
  `packages/config/src/vite.ts` that states why. The first download's limit is now 170 kB (UX-123).

## Consequences

- The first download fell to 138 kB gzip and no longer includes the frame.
- The budget is in gzip bytes, but the control plane sends assets uncompressed today;
  UX-121 makes it serve precompressed copies.
- A page that needs a large library has to load it with that page, not from a shared
  index.
