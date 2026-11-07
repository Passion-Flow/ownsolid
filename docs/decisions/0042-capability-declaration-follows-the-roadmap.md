# 0042: The capability declaration lives in one file and follows the roadmap

Date: 2026-11-07. Status: accepted.

## Context

[ADR 0036](0036-show-every-option-and-fetch-models-offline.md) decided that every option stays visible with
a state and a reason, and that the installer, the API and the console read one declaration. It left open where
that declaration lives, and how "moving a row to `ready` needs the tick first" is held. The demo's list marks
vLLM, SGLang and every model source as ready, while their roadmap items are open.

## Decision

- **The declaration is `pkg/capabilities/declaration.json`,** embedded in every program that reads it. The
  installer and the control plane take their topology sizes from it; the API serves it to anyone signed in; the
  console and the portal show it at `/support`. Text is English there and translated in `locales/`, and a test
  holds the two together.
- **A test holds each row to the roadmap.** `ready` and `licensed` name a ticked item, `planned` an open one, and
  `unsupported` none. A tick that leaves its row behind fails `make test`, whichever lane makes it. So on the
  first day the page tells the truth: most rows are planned, including NVIDIA and vLLM, whose code exists but
  whose items are open.
- **A roadmap item is shown, not linked.** S-106 asked for a link to a public item. The repository is private, no
  public roadmap exists, publishing one is the owner's decision, and an air-gapped console could not open it. The
  ID can be copied, and the reason says what is missing.

## Alternatives

A TypeScript list could not reach the installer. Generated constants solve a problem only topology sizes have, and
a test covers those. Rows set per installation would answer a different question: this one is about the release.
[The research note](../research/capability-registry.md) has the detail.

## Consequences

- Each lane that ticks a named item edits the declaration and its two catalogs in the same change.
- A buyer sees a list that is mostly planned until P1 lands. That is what ADR 0036 asked for, and it shortens as
  items are ticked.
- CMP-216 builds the control for an unavailable option when the first form offers a registry's choices.
