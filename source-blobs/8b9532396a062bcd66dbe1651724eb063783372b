# Undo and confirmation

Research behind UX-105: reversible actions offer undo, and only irreversible ones ask the
person to type a confirmation.

## How others solve it

- **AWS Cloudscape** ([delete patterns](https://cloudscape.design/patterns/resource-management/delete/),
  [additional confirmation](https://cloudscape.design/patterns/resource-management/delete/delete-with-additional-confirmation/)):
  three levels. One-click for things with no effect on what is running; a simple confirmation
  for what cannot be quickly recreated; written confirmation when a deletion "could have
  serious, irreversible or cascading consequences". The title names the action, the body says
  it cannot be undone and names the resource, consequences go in a warning, and the action
  button repeats the verb.
- **GitHub** ([deleting a repository](https://docs.github.com/en/repositories/creating-and-managing-repositories/deleting-a-repository)):
  the person types the repository's name, "to verify that you're deleting the correct
  repository", after reading the effects.
- **Carbon** ([dialog pattern](https://carbondesignsystem.com/patterns/dialog-pattern/),
  [modal](https://carbondesignsystem.com/building-blocks/core/components/modal/guidelines)):
  a danger modal for destructive or irreversible actions; descriptive button labels; Cancel on
  the left and one primary action on the right; focus goes to the first input; Escape, the
  close button and Cancel all leave it; "don't overuse dialogs".
- The product's own principle 4 prefers undo to confirmation. The prototype's toast offers
  Undo for six seconds. NN/g's and Material's pages could not be read (one refused, one
  rendered nothing), so neither is cited.

## Choices

- **Three levels, decided by what happens if the person is wrong.**
  1. **Reversible:** do it at once and offer Undo in the toast. Renaming, saving a view's
     changes, deleting a view and changing the default language all restore exactly.
  2. **Not reversible, but cheap or already gated:** do it, with no dialog. Sending an email
     again cannot be recalled but harms nothing. Unlinking a provider asks for the password,
     which is a stronger check than typing.
  3. **Irreversible with real consequences:** `TypedConfirmation`. Revoking an invitation ends
     a link that cannot be brought back.
- **The person types the target's own name,** as GitHub asks, rather than Cloudscape's word
  `confirm`: it proves they read which one, and needs no translation. Case and surrounding
  spaces do not matter, because a slip of the shift key is not the mistake this guards against.
- **The action button stays disabled until the text matches,** and is the danger style. Focus
  starts in the field, and Enter submits only when it matches.
- **Undo is a real write.** It sends the previous value on top of the revision the save
  returned, so it fails rather than overwriting a newer change; the failure says so and offers
  a retry.
- **A dialog is never added to a reversible action,** so people keep reading the ones that
  remain.

## Licences

No new dependency.

## Failure modes and tests

- Undo after someone else changed the record: the revision check refuses it, and the toast
  says the change could not be undone. Browser test.
- Typing the wrong name or nothing: the button stays disabled, and Enter does nothing.
- Revoking an invitation that was accepted meanwhile: `INVITATION_STATE_CONFLICT` explains it
  and the list reloads. Browser test.
- A revoked invitation offers no second revoke and no resend. Browser test.
- The mail list joins the invitation for each row: covered for each kind of email by a server
  test against PostgreSQL.

## Done when

Saving a display name, a default language, a view's changes and a view's name offers Undo that
restores the previous value; revoking an invitation from its email requires typing the address;
and the rule is in the design system, so later items choose the level the same way.
