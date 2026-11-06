# 0029: Show every option, and get models in without a network

Date: 2026-11-07. Status: accepted.

Two decisions that arrived together, because they answer the same question: how does
a product that is not finished avoid either lying about what it does or looking
smaller than it is.

## 1. Every option is shown; only the ready ones can be chosen

A buyer asks whether the platform supports Windows nodes, or SGLang, or Ascend.
Hiding what is not built yet makes the product look narrow and forces the question
into a sales call. Showing it as though it works is worse.

**An option that is not ready stays in the list, cannot be chosen, and says why.**

The research behind the presentation:

- The U.S. Web Design System's work on disabled states is explicit that a disabled
  control legitimately shows that an option exists but is not currently available.
  That is exactly this case, and it is the one case where disabling is right.
- It carries `aria-disabled="true"`, not the `disabled` attribute, so assistive
  technology still reports the option is there. Removing it from the tree would
  tell a screen-reader user the option does not exist.
- **It always carries a reason.** A control that does nothing when clicked, with no
  feedback and no explanation, is the most-cited failure of disabled states. The
  contrast has to stay readable too: an unreadable reason is no reason.

Four states, in `src/content/capabilities.ts` in the demo and in the product's own
configuration when this reaches it:

| State | Meaning |
| --- | --- |
| `ready` | Implemented and usable now. |
| `planned` | Will be supported. Names the roadmap item that would make it ready. |
| `licensed` | Works, under a licence or account the customer holds. |
| `unsupported` | A decision not to support it, with the reasoning. Never silently dropped. |

Moving a row to `ready` is a claim, and the roadmap item behind it has to be ticked
first. Adding a row does not make anything work.

## 2. The model catalogue is an index, and weights arrive in one verified bundle

Importing a model by hand, one file at a time, is offline but it is not good. Nobody
finds a model that way. The fix is not to give up on offline: it is to notice that
**searching does not need the network, only fetching does.**

1. **The catalogue is an index**: names, publishers, parameter counts, context
   lengths, licences, tags and hashes. No weights. An index of both registries is
   metadata, so it ships with the product and refreshes from a signed bundle.
2. **Search and filter run offline**, against that index, on the installation itself.
3. **You tick what you want** and export a fetch manifest: exact repositories, exact
   revisions, expected hashes. Kilobytes.
4. **One command on a connected machine** turns the manifest into one bundle.
5. **Import once.** Every model is checked against the hash its registry published
   before anything is written.

This is the two-phase shape NVIDIA's air-gap documentation describes — a connected
phase that builds a store, then a transfer — with the browsing moved to the offline
side, where the person actually is.

Three acquisition modes, because customers differ: air-gapped transfer as above, a
relay mirror in a DMZ, and direct download for development.

### Two things the research turned up that have to be handled

- **A serving engine can still phone home.** vLLM looks for tokenizer files on
  Hugging Face when they were not staged with the weights. The bundle has to include
  them, and the installation blocks outbound traffic, so a model missing a file
  fails loudly instead of quietly reaching the internet.
- **Gated models need a token on the connected side.** The token belongs to the
  machine that fetches, never to the air-gapped installation.

### Registries

Hugging Face has a documented listing API behind `huggingface_hub`'s `list_models`,
with `search`, `author`, `filter`, `sort` and `limit`. ModelScope has no equally
documented REST listing route; its Python SDK is the supported path, and an index
builder uses that. Both are used only on the connected side.

## What this changes about the demo

The demo stops showing invented rows. A new installation has no models, and the
model library says so, with the two things that would change it. Catalogue entries
are real models on real registries. Sizes are estimated from parameter counts at two
bytes per parameter and labelled as estimates; licences are what the registry
reports, and the product does not reinterpret them.

## Consequences

- A capability registry becomes a real configuration surface, not a UI detail: the
  installer, the API and the console all read the same declaration.
- Every form that offers a choice is rebuilt to take it from that registry.
- Empty states replace sample data across the product, page by page.
