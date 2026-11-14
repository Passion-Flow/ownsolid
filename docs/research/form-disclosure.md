# Advanced settings in a form

Research behind UX-125. [ADR 0037](../decisions/0037-simple-on-the-surface-deep-underneath.md) decides the rule: one
fold per form, a field is advanced when it has a default that works, the fold says how much is behind it, and
anything without a safe default, that costs money or capacity, or that cannot be undone never goes behind it.
[The design system of record](../product/design/README.md) repeats it. This note records how it is built.

## Choices

- **`details` and `summary`.** The [HTML element](https://html.spec.whatwg.org/multipage/interactive-elements.html#the-details-element)
  brings the role, the state and the keyboard, so nothing needs writing and testing again.
- **Its label carries the count**, "Advanced settings (3)", which is the whole of what the ADR asks of it.
- **A problem inside opens it and keeps it open.** Otherwise a disabled Save would have no visible reason. Forms
  here validate as a person types, so this covers a value made invalid and then folded away.
- **Closing it keeps the values.** The fields stay mounted, so a change made inside still saves.
- **It holds no state beyond the page.** It starts closed each time, so a form looks the same on every visit.

## Where it applies today

Only Agent pacing has fields that qualify and enough of them. Jitter and both retry waits work at their defaults,
and the heartbeat is what a person comes to change. The other forms have nothing to fold:

- Add a node, Create a workspace, Add a member: every field is needed, or chooses where capacity goes.
- Invite someone: an address and a language. A fold around a single field costs as much as the field.
- Single sign-on: a name, an origin, a client ID, a secret and a reason, none of which has a working default.

New forms apply the rule as they arrive; module items such as deploying a model are where it will matter most.

Rejected: an automatic check that every form declares its fields, which would reject short forms for having no fold;
a fold remembered per person, which would make one person's form differ from the screenshot in the documentation.

## Licences

No new dependency.

## Failure modes and tests

- A value made invalid and folded away stays in view; browser flow.
- A value changed and folded away still saves; browser flow.
- Reached and opened from the keyboard, and axe passes open and closed; browser flow.
- The Reason field and the heartbeat are never behind the fold; browser flow.

## What the finished item must do

Agent pacing shows the heartbeat and the reason, with three fields behind one fold that says so.
