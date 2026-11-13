import { describe, expect, it } from 'vitest';
import {
  activitySearch,
  activitySelection,
  activityTimeFromField,
  customActivityWindow,
  localActivityTime,
  recentActivityWindow,
} from './activity-window.ts';

describe('bounded resource activity dates', () => {
  it('uses 24 elapsed hours even across daylight-saving changes', () => {
    const to = '2026-03-08T18:00:00.123Z';
    expect(recentActivityWindow(Date.parse(to))).toEqual({ from: '2026-03-07T18:00:00.123Z', to });
  });

  it('keeps the tab and unrelated URL values when applying and clearing a range', () => {
    const selection = {
      kind: 'custom',
      from: '2026-01-01T00:00:00Z',
      to: '2026-01-02T00:00:00Z',
    } as const;
    const search = activitySearch('?tab=actions&open=a', selection);
    expect(activitySelection(search)).toEqual(selection);
    expect(new URLSearchParams(search).get('tab')).toBe('actions');
    expect(activitySearch(search, { kind: 'recent' })).toBe('?tab=actions&open=a');
    expect(activitySelection('')).toEqual({ kind: 'recent' });
    expect(activitySelection('?activity_from=invalid')).toEqual({
      kind: 'custom',
      from: 'invalid',
      to: '',
    });
  });

  it.each([
    ['2026-02-30T00:00:00Z', '2026-03-03T00:00:00Z'],
    ['invalid', '2026-03-03T00:00:00Z'],
    ['2026-03-03T00:00:00Z', '2026-03-03T00:00:00Z'],
    ['2026-03-04T00:00:00Z', '2026-03-03T00:00:00Z'],
    ['0999-03-01T00:00:00Z', '2026-03-03T00:00:00Z'],
  ])('refuses invalid or reversed URL bounds %s / %s', (from, to) => {
    expect(customActivityWindow({ from, to })).toBeNull();
  });

  it('leaves deployment-specific maximum validation to the server', () => {
    expect(
      customActivityWindow({ from: '2026-01-01T00:00:00Z', to: '2026-03-01T00:00:00Z' }),
    ).toEqual({
      from: '2026-01-01T00:00:00.000Z',
      to: '2026-03-01T00:00:00.000Z',
    });
  });

  it('round-trips local fields with milliseconds without assuming the browser time zone', () => {
    const instant = '2026-11-13T10:15:25.123Z';
    expect(activityTimeFromField(localActivityTime(instant))).toBe(instant);
    expect(activityTimeFromField('2026-02-30T12:00')).toBeNull();
    expect(activityTimeFromField('2026-11-13T24:00')).toBeNull();
    expect(activityTimeFromField('')).toBeNull();
    expect(localActivityTime('2026-02-30T00:00:00Z')).toBe('');
  });
});
