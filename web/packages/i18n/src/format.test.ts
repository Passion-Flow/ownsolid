import { describe, expect, it } from 'vitest';
import { createFormatter } from './format.ts';

const moment = '2026-11-04T20:05:00Z';
const english = createFormatter('en', 'UTC');
const chinese = createFormatter('zh-CN', 'Asia/Shanghai');

describe('dates', () => {
  it('follows the language', () => {
    expect(english.date(moment)).toBe('Nov 4, 2026');
    expect(chinese.date(moment)).toBe('2026\u5e7411\u67085\u65e5');
    expect(english.time(moment)).toMatch(/^8:05\sPM$/);
    expect(chinese.time(moment)).toBe('04:05');
    expect(english.dateTime(moment)).toMatch(/^Nov 4, 2026, 8:05\sPM$/);
    expect(chinese.dateTime(moment)).toBe('2026\u5e7411\u67085\u65e5 04:05');
  });

  it('accepts a Date as well as the API string', () => {
    expect(english.date(new Date(moment))).toBe('Nov 4, 2026');
  });

  it('shows a dash for a date it cannot read', () => {
    expect(english.date('not a date')).toBe('-');
    expect(english.dateTime('')).toBe('-');
  });

  it('uses the browser time zone unless told otherwise', () => {
    const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
    expect(createFormatter('en').timeZone).toBe(zone);
    expect(english.timeZone).toBe('UTC');
    expect(chinese.timeZone).toBe('Asia/Shanghai');
    expect(createFormatter('en').dateTime(moment)).toBe(
      createFormatter('en', zone).dateTime(moment),
    );
  });
});

describe('numbers', () => {
  it('groups by language', () => {
    expect(english.number(1234567.5)).toBe('1,234,567.5');
    expect(createFormatter('de').number(1234567.5)).toBe('1.234.567,5');
  });

  it('formats a ratio as a percentage', () => {
    expect(english.percent(0.42)).toBe('42%');
    expect(english.percent(0.1234)).toBe('12.3%');
    expect(chinese.percent(1)).toBe('100%');
  });

  it('shows a dash for what is not a number', () => {
    for (const format of [english.number, english.percent, english.bytes, english.duration]) {
      expect(format(Number.NaN)).toBe('-');
      expect(format(Number.POSITIVE_INFINITY)).toBe('-');
    }
    expect(english.countdown(Number.NaN)).toBe('-');
  });
});

describe('sizes', () => {
  it('chooses the unit that reads best', () => {
    expect(english.bytes(0)).toBe('0 byte');
    expect(english.bytes(999)).toBe('999 byte');
    expect(english.bytes(1_000)).toBe('1 kB');
    expect(english.bytes(65_500_000_000)).toBe('65.5 GB');
    expect(english.bytes(214_000_000_000)).toBe('214 GB');
    expect(english.bytes(2_500_000_000_000)).toBe('2.5 TB');
    expect(english.bytes(3_000_000_000_000_000_000)).toBe('3,000 PB');
  });

  it('does not stop just short of the next unit', () => {
    expect(english.bytes(999_950)).toBe('1 MB');
    expect(english.bytes(999_940)).toBe('999.9 kB');
  });

  it('keeps the sign', () => {
    expect(english.bytes(-1_500)).toBe('-1.5 kB');
  });

  it('follows the language', () => {
    expect(chinese.bytes(65_500_000_000)).toBe('65.5 GB');
  });
});

describe('durations', () => {
  it('shows the two largest units', () => {
    expect(english.duration(0)).toBe('0s');
    expect(english.duration(45)).toBe('45s');
    expect(english.duration(60)).toBe('1m');
    expect(english.duration(526)).toBe('8m 46s');
    expect(english.duration(3_900)).toBe('1h 5m');
    expect(english.duration(90_000)).toBe('1d 1h');
  });

  it('leaves out a zero second unit rather than skipping to a third', () => {
    expect(english.duration(3_605)).toBe('1h');
    expect(english.duration(86_460)).toBe('1d');
  });

  it('rounds, and treats a negative length as none', () => {
    expect(english.duration(59.6)).toBe('1m');
    expect(english.duration(-5)).toBe('0s');
  });

  it('follows the language', () => {
    expect(chinese.duration(3_900)).toBe('1\u5c0f\u65f65\u5206\u949f');
  });
});

describe('countdown', () => {
  it('counts in minutes and seconds', () => {
    expect(english.countdown(0)).toBe('0:00');
    expect(english.countdown(65)).toBe('1:05');
    expect(english.countdown(3_600)).toBe('60:00');
    expect(english.countdown(-1)).toBe('0:00');
  });
});
