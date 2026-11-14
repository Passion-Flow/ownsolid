// Reading a cursor list whole, up to a bound, for a page that must count or filter every row the
// server has no filter for.

/** How many pages one read takes at most. */
export const maximumPages = 10;

/** Rows per page, the most a list answers. */
export const pageSize = 100;

/**
 * Reads pages until the list ends or {@link maximumPages} were read; `complete` says which, so a
 * page can say that what it counts is not everything.
 */
export async function everyPage<T>(
  read: (cursor: string | undefined) => Promise<{ items: T[]; next_cursor: string | null }>,
): Promise<{ items: T[]; complete: boolean }> {
  const items: T[] = [];
  let cursor: string | undefined;
  for (let page = 0; page < maximumPages; page += 1) {
    const answer = await read(cursor);
    items.push(...answer.items);
    if (!answer.next_cursor) return { items, complete: true };
    cursor = answer.next_cursor;
  }
  return { items, complete: false };
}
