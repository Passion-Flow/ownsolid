// Moving through the rows of a list from the keyboard (UX-128). Focus is the selection: it lands on the
// control that opens the row, so Enter opens it and a screen reader says which row it is.

// The first of these in a row is what opens it; a checkbox before it only selects for bulk actions.
const opener = 'a[href], button';

/** Moves to the next or previous row of the page's lists. False when the page has none. */
export function moveThroughRows(direction: 1 | -1): boolean {
  const openers = [...document.querySelectorAll('main table tbody tr')].flatMap((row) => {
    const control = row.querySelector<HTMLElement>(opener);
    return control ? [{ row, control }] : [];
  });
  if (openers.length === 0) return false;
  const here = openers.findIndex(({ row }) => row.contains(document.activeElement));
  // From outside the list, either key lands on the end it faces. At an end, a key stays put.
  const next =
    here === -1
      ? direction === 1
        ? 0
        : openers.length - 1
      : Math.min(Math.max(here + direction, 0), openers.length - 1);
  openers[next]?.control.focus();
  return true;
}
