# Resource activity bounded history research draft

Status: read-only follow-up, no product implementation, installed run or roadmap tick.

## Source findings

The existing resource activity component requests creation minus one minute through
server now. The current parser accepts a maximum configured window and pins both
bounds in its cursor. The default is31days; valid deployment maxima range1to366days.
A31day-old resource already exceeds the default by that extra minute. The prior
whole-lifetime statement in resource-detail-pages.md is therefore not generally true.
No public runtime projection of those audit policy bounds was found in the current
Web/OpenAPI inputs. A client maximum must not be guessed.

## Proven interface patterns and planned approach

Grafana exposes relative windows and explicit absolute bounds; Kibana uses a time
filter with explicit Apply for custom bounds. These are interaction references,
not imported or redistributed code:

- https://grafana.com/docs/grafana/latest/visualizations/dashboards/use-dashboards/
- https://www.elastic.co/docs/explore-analyze/query-filter/filtering

Keep the existing resource detail/tab archetype. Add a bounded-history choice with
explicit Apply rather than an automatic multi-window scan. Preserve older history
through absolute date/time selection; never silently replace the lifetime promise
with an unlabeled recent slice. The default must fit all valid server policies or
use the server default. Exact selected bounds, active query cache and cursor bounds
must agree across remount, language change and explicit recovery. Native local-time
fields reuse existing locale formatting and the existing API; no new dependency,
model or dataset is needed. The exact implementation still needs an isolated
prototype for cached/default windows before product code is written.

Reject a hardcoded31day deployment maximum, a creation-to-now query, silently
clamping historical data, automatically walking every old time window, weakening
the server maximum, and retrying a failed read on filter revisits. None preserves
both configurable policy and the explicit-read guarantees just qualified.

## Required verification

Use an older-than-policy resource and a current event, with a policy-faithful API
fixture as the negative control. Test minimum1day and default31day maxima; an old
absolute window; no events in the default window versus no matches for selected
dates; malformed/reversed/out-of-policy bounds; cursor next-page/restart; response
loss; no focus/reconnect/invalidation replay; cached failed filter revisits;
revoked permissions; and member/foreign workspace refusal. Apply and recovery must
each produce only their one explicit request. Preserve partial pages.

Cover EN/ZH, keyboard,390px and desktop, every applicable state, and inspect the
resulting screenshots. Actual installed browser and same-source server evidence
remain required for acceptance. The boundary calculation beside this draft is only
static source analysis and must never be reported as a running server result.
