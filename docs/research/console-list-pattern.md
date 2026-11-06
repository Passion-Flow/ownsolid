# Console list pattern

Research behind UX-103 (filter bar, saved views, column chooser, bulk selection with action
bar, detail drawer) and the related UX-114 (shareable links). Every list page in the console
and portal builds on it, so it is settled once here.

## How others solve it

- **Carbon** ([data table usage](https://carbondesignsystem.com/components/data-table/usage/)):
  once a row is selected a batch action bar replaces the toolbar and offers actions for all
  selected rows; row actions are disabled meanwhile; a cancel button or deselecting every row
  leaves batch mode. Multi-select uses checkboxes, and the header checkbox has a third,
  indeterminate state. The toolbar holds at most five global actions (settings, filters,
  export); search is collapsed or always open.
- **WAI-ARIA Authoring Practices** ([table](https://www.w3.org/WAI/ARIA/apg/patterns/table/),
  [grid](https://www.w3.org/WAI/ARIA/apg/patterns/grid/)): a list of records is a static
  table, built from native `table` elements, with each checkbox, link or button a separate
  tab stop. A grid, with one tab stop and arrow keys, is for editable or spreadsheet-like data
  and is worth it only when a table has very many widgets. A sorted column carries
  `aria-sort`; hidden columns or partial rows use `aria-colcount` and `aria-rowcount`.
- **GitHub Projects**
  ([managing views](https://docs.github.com/en/issues/planning-and-tracking-with-projects/customizing-views-in-your-project/managing-your-views)):
  a view saves filter, sort, grouping and order. Changing them marks the view as having
  unsaved changes, which no one else sees, until the person saves. New views and renames
  save at once.
- **AWS Cloudscape** ([table](https://cloudscape.design/components/table/),
  [collection preferences](https://cloudscape.design/components/collection-preferences/),
  [property filter](https://cloudscape.design/components/property-filter/)): display
  preferences (visible columns, page size) are a separate control from filtering, and filters
  combine properties, values and free text. The fetched pages did not state how preferences or
  saved filter sets persist.

## Choices

- **Native table, not a grid.** The lists are records with a checkbox and a few buttons per
  row, which APG says stays a table. The keyboard flows already check every control is a tab
  stop with visible focus.
- **Saved views live on the server, per person and per list.** A view must follow the account
  across browsers, like the theme and language (ADR 0019, ADR 0025), and the owner's
  installations are shared workstations as often as personal ones. A view stores the list's
  filters, sort and visible columns as a small JSON object the page owns; the server checks its
  size and shape, not its meaning. Views are personal; sharing one is UX-114's link.
- **Unsaved changes are shown, as GitHub does.** Changing a filter while a view is open marks
  the view as changed, with save and revert.
- **Columns are a preference of the list, remembered in this browser,** and saved with a view.
  A column that holds the row's name cannot be hidden.
- **Bulk selection covers the rows on screen,** with a header checkbox in three states. The
  action bar appears at the bottom of the page, as in the approved prototype, says how many
  rows are selected, offers the actions that apply to every one of them, and Escape or Clear
  ends it. An action applies to each selected row and reports how many succeeded; a refusal
  of one row does not undo the others.
- **The detail drawer is the existing `Drawer`,** opened from a row's name. Escape closes it
  and returns focus to the row.
- **No table library.** TanStack Table (MIT) would model selection and visibility, but the
  lists are server-paged and need only these few states; its code would weigh on every page's
  budget for logic of a few dozen lines.

## Licences

No new dependency. The server change uses PostgreSQL only.

## Failure modes and tests

- A saved view from an older page names a column or filter the page no longer has: the page
  ignores what it does not know. Unit test.
- Two browsers change one view: updates carry a revision and fail with
  `PREFERENCE_REVISION_CONFLICT`; the page reloads the views. Server test with concurrent
  writers.
- A person reaches another person's view by ID: every read and write is scoped to the session's
  user, and a foreign ID answers not found. Server test.
- A view larger than allowed, or too many views on one list: refused with
  `VALIDATION_FAILED` and `RESOURCE_LIMIT_REACHED`. Server test.
- A bulk action where some rows fail: the toast reports the count, failed rows stay selected,
  and retrying sends only those. Browser test.
- Selection survives a filter change only for rows still shown. Unit test.
- MySQL and Dameng have no table for views: they refuse with `PREFERENCE_UNAVAILABLE`, and the
  page still works without views.

## Done when

On the email delivery page, a person filters by status, purpose and recipient, hides and shows
columns, saves the result as a named view, opens it in another browser, renames and deletes
it, selects several failed emails and sends them again in one action, and opens an email's
details in a drawer. Every state passes the accessibility and keyboard flows in both themes
and both languages, and the pieces are documented in the design system for the next pages.
