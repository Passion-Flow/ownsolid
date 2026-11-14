import { describe, expect, it } from 'vitest';
import {
  emptyListState,
  linksList,
  readListLink,
  writeListLink,
  readListState,
  sameListState,
  selectedOnScreen,
  withColumn,
  withFilter,
  type ListDefinition,
} from './list-state.ts';

const definition: ListDefinition = {
  key: 'things',
  filters: {
    state: { kind: 'choice', values: ['failed', 'sent'] },
    name: { kind: 'text', maximum: 5 },
  },
  columns: [
    { id: 'name', label: 'mail.column.recipient', locked: true },
    { id: 'state', label: 'mail.column.state' },
    { id: 'size', label: 'mail.column.attempts' },
  ],
};

describe('readListState', () => {
  it('keeps what the page still has', () => {
    expect(
      readListState(
        { filters: { state: 'failed', name: 'ada' }, hiddenColumns: ['size'] },
        definition,
      ),
    ).toEqual({ filters: { state: 'failed', name: 'ada' }, hiddenColumns: ['size'] });
  });

  it('drops filters and values the page no longer knows', () => {
    expect(
      readListState(
        { filters: { state: 'gone', other: 'x', name: 'too long for it', empty: '' } },
        definition,
      ).filters,
    ).toEqual({});
  });

  it('drops unknown columns and never hides a locked one', () => {
    expect(
      readListState({ hiddenColumns: ['name', 'removed', 'state'] }, definition).hiddenColumns,
    ).toEqual(['state']);
  });

  it.each([null, undefined, 'text', 3, [], { filters: [], hiddenColumns: 'state' }])(
    'reads %j as nothing chosen',
    (stored) => {
      expect(readListState(stored, definition)).toEqual(emptyListState);
    },
  );
});

describe('sameListState', () => {
  it('ignores the order things were chosen in', () => {
    expect(
      sameListState(
        { filters: { a: '1', b: '2' }, hiddenColumns: ['x', 'y'] },
        { filters: { b: '2', a: '1' }, hiddenColumns: ['y', 'x'] },
      ),
    ).toBe(true);
  });

  it('sees a difference in filters or columns', () => {
    expect(sameListState(emptyListState, withFilter(emptyListState, 'a', '1'))).toBe(false);
    expect(sameListState(emptyListState, withColumn(emptyListState, 'a', false))).toBe(false);
  });
});

describe('changes', () => {
  it('clears a filter set to nothing', () => {
    const set = withFilter(emptyListState, 'state', 'failed');
    expect(set.filters).toEqual({ state: 'failed' });
    expect(withFilter(set, 'state', '').filters).toEqual({});
  });

  it('shows and hides a column once', () => {
    const hidden = withColumn(withColumn(emptyListState, 'a', false), 'a', false);
    expect(hidden.hiddenColumns).toEqual(['a']);
    expect(withColumn(hidden, 'a', true).hiddenColumns).toEqual([]);
  });
});

describe('selectedOnScreen', () => {
  it('forgets rows that left the screen', () => {
    expect([...selectedOnScreen(new Set(['a', 'b']), ['b', 'c'])]).toEqual(['b']);
  });

  it('keeps the same set when nothing changed, so nothing renders again', () => {
    const selected = new Set(['a']);
    expect(selectedOnScreen(selected, ['a', 'b'])).toBe(selected);
  });
});

describe('list links', () => {
  it('reads filters and the open row, checking them like a saved view', () => {
    expect(readListLink('?state=failed&name=ada&open=abc-1&other=x', definition)).toEqual({
      filters: { state: 'failed', name: 'ada' },
      open: 'abc-1',
    });
    expect(readListLink('?state=gone&name=far+too+long&open=a%20b', definition)).toEqual({
      filters: {},
      open: null,
    });
  });

  it('keeps text that looks like a number as text', () => {
    expect(readListLink('?name=123', definition).filters).toEqual({ name: '123' });
  });

  it('writes a stable link and keeps parameters it does not own', () => {
    const link = writeListLink(
      '?other=x&state=sent',
      definition,
      { state: 'failed', name: 'a b' },
      'r1',
    );
    expect(link).toBe('?other=x&name=a+b&state=failed&open=r1');
    expect(writeListLink(link, definition, {}, null)).toBe('?other=x');
    expect(writeListLink('', definition, {}, null)).toBe('');
  });

  it('round-trips', () => {
    const filters = { state: 'sent', name: 'gr' };
    expect(readListLink(writeListLink('', definition, filters, 'x'), definition)).toEqual({
      filters,
      open: 'x',
    });
  });

  it('knows whether a link names the list', () => {
    expect(linksList('?other=x', definition)).toBe(false);
    expect(linksList('?state=failed', definition)).toBe(true);
    expect(linksList('?open=x', definition)).toBe(true);
  });
});
