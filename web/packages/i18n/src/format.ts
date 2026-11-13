// Dates, numbers and units for the interface language. Every page formats through here, so
// they stay consistent and follow the language the moment it changes. A lint rule keeps
// direct Intl and toLocaleString calls out of the pages.

/** A moment as the API sends it, or as a page holds it. */
type Moment = Date | string;

/** What {@link createFormatter} returns. Bad input shows as a dash, never as an exception. */
export interface Formatter {
  /** The time zone used by the displayed dates and native local date/time fields. */
  timeZone: string;
  /** A calendar date: Nov 4, 2026. */
  date: (value: Moment) => string;
  /** A date and time: Nov 4, 2026, 8:00 AM. */
  dateTime: (value: Moment) => string;
  /** A time of day: 8:00 AM. */
  time: (value: Moment) => string;
  /** A plain number with the language's grouping: 1,234.5. */
  number: (value: number) => string;
  /** A ratio from 0 to 1 as a percentage: 42%. */
  percent: (ratio: number) => string;
  /** A size in decimal units, as disks and model files are sold: 65.5 GB. */
  bytes: (value: number) => string;
  /** A length of time in its two largest units: 1h 5m. */
  duration: (seconds: number) => string;
  /** Time left on a running clock: 1:05. */
  countdown: (seconds: number) => string;
}

const missing = '-';
const byteUnits = ['byte', 'kilobyte', 'megabyte', 'gigabyte', 'terabyte', 'petabyte'] as const;
const durationUnits = [
  ['day', 86_400],
  ['hour', 3_600],
  ['minute', 60],
  ['second', 1],
] as const;

/**
 * Creates the formatters for a language. Times show in the browser's time zone unless
 * `timeZone` names another; tests name one so they pass anywhere.
 */
export function createFormatter(locale: string, timeZone?: string): Formatter {
  const zone = timeZone ? { timeZone } : {};
  const moments = (options: Intl.DateTimeFormatOptions) => {
    const format = new Intl.DateTimeFormat(locale, { ...options, ...zone });
    return (value: Moment) => {
      const moment = new Date(value);
      return Number.isNaN(moment.getTime()) ? missing : format.format(moment);
    };
  };
  const finite = (format: (value: number) => string) => (value: number) =>
    Number.isFinite(value) ? format(value) : missing;

  const plain = new Intl.NumberFormat(locale);
  const whole = new Intl.NumberFormat(locale, { useGrouping: false, maximumFractionDigits: 0 });
  const twoDigits = new Intl.NumberFormat(locale, {
    useGrouping: false,
    minimumIntegerDigits: 2,
    maximumFractionDigits: 0,
  });
  const percent = new Intl.NumberFormat(locale, { style: 'percent', maximumFractionDigits: 1 });
  const sizes = byteUnits.map(
    (unit) =>
      new Intl.NumberFormat(locale, {
        style: 'unit',
        unit,
        unitDisplay: 'short',
        maximumFractionDigits: unit === 'byte' ? 0 : 1,
      }),
  );
  const length = (unit: (typeof durationUnits)[number][0]) =>
    new Intl.NumberFormat(locale, {
      style: 'unit',
      unit,
      unitDisplay: 'narrow',
      maximumFractionDigits: 0,
    });
  const lengths = durationUnits.map(([unit, seconds]) => [seconds, length(unit)] as const);
  const parts = new Intl.ListFormat(locale, { style: 'narrow', type: 'unit' });

  return {
    timeZone: new Intl.DateTimeFormat(locale, zone).resolvedOptions().timeZone,
    date: moments({ dateStyle: 'medium' }),
    dateTime: moments({ dateStyle: 'medium', timeStyle: 'short' }),
    time: moments({ timeStyle: 'short' }),
    number: finite((value) => plain.format(value)),
    percent: finite((ratio) => percent.format(ratio)),
    bytes: finite((value) => {
      let scaled = Math.abs(value);
      let index = 0;
      // Round first, so 999,950 bytes reads 1 MB rather than 1,000 kB.
      while (Math.round(scaled * 10) / 10 >= 1_000 && index < sizes.length - 1) {
        scaled /= 1_000;
        index += 1;
      }
      return (sizes[index] ?? plain).format(Math.sign(value) * scaled);
    }),
    duration: finite((seconds) => {
      const total = Math.max(0, Math.round(seconds));
      // The largest unit that fits, then the next one down when it is not zero: 1h 5m.
      for (const [index, [size, format]] of lengths.entries()) {
        if (total < size) continue;
        const used = [format.format(Math.floor(total / size))];
        const next = lengths[index + 1];
        const rest = next ? Math.floor((total % size) / next[0]) : 0;
        if (next && rest > 0) used.push(next[1].format(rest));
        return parts.format(used);
      }
      return length('second').format(0);
    }),
    countdown: finite((seconds) => {
      const left = Math.max(0, Math.round(seconds));
      return `${whole.format(Math.floor(left / 60))}:${twoDigits.format(left % 60)}`;
    }),
  };
}
