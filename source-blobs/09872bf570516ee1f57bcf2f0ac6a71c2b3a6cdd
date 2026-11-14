/** Local browser limits for enrollment drafts. Control independently enforces token capacity. */
export const enrollmentCsvLimits = { bytes: 128 * 1024, rows: 100 } as const;

export interface EnrollmentCsvRow {
  line: number;
  name: string;
  rack: string;
  issue?: 'columns' | 'name' | 'rack' | 'duplicate';
}

type CsvIssue = 'size' | 'syntax' | 'header' | 'rows' | 'empty';
type CsvResult = { rows: EnrollmentCsvRow[] } | { issue: CsvIssue; line?: number };

// Sticky matching keeps the next delimiter adjacent to the field. A bare quote or an unclosed
// quoted field can never be skipped. The final empty field after a comma is significant.
function* records(source: string) {
  const field = /(?:"((?:[^"]|"")*)"|([^",\r\n]*))(,|\r\n|\n|$)/y;
  let line = 1;
  let start = 1;
  let values: string[] = [];
  while (field.lastIndex < source.length || values.length > 0) {
    const match = field.exec(source);
    if (!match) {
      yield { line: start, values: null };
      return;
    }
    const [raw, quoted, plain, separator] = match;
    values.push(quoted === undefined ? (plain ?? '') : quoted.replaceAll('""', '"'));
    line += raw.split('\n').length - 1;
    if (separator !== ',') {
      yield { line: start, values };
      start = line;
      values = [];
    }
  }
}

function validLabel(value: string) {
  return Array.from(value).length <= 100 && !/[\p{Cc}]/u.test(value);
}

/** Parses a local UTF-8 file before any request can be sent. */
export function parseEnrollmentCsv(input: string): CsvResult {
  const source = input.replace(/^\uFEFF/, '');
  const entries = records(source);
  const header = entries.next().value;
  if (!header) return { issue: 'empty' };
  if (!header.values) return { issue: 'syntax', line: header.line };
  const columns = header.values;
  const nameIndex = columns.indexOf('node_name');
  const rackIndex = columns.indexOf('rack');
  if (nameIndex < 0 || (columns.length !== 1 && (columns.length !== 2 || rackIndex < 0))) {
    return { issue: 'header' };
  }

  const rows: EnrollmentCsvRow[] = [];
  const names = new Map<string, EnrollmentCsvRow>();
  for (const { line, values } of entries) {
    if (!values) return { issue: 'syntax', line };
    if (values.length === 1 && values[0] === '') continue;
    if (rows.length >= enrollmentCsvLimits.rows) return { issue: 'rows' };
    const name = (values[nameIndex] ?? '').trim().normalize();
    const rack = (values[rackIndex] ?? '').trim().normalize();
    const row: EnrollmentCsvRow = { line, name, rack };
    if (values.length !== columns.length) row.issue = 'columns';
    else if (!name || !validLabel(name)) row.issue = 'name';
    else if (!validLabel(rack)) row.issue = 'rack';
    else {
      const key = name.toLowerCase();
      const earlier = names.get(key);
      if (earlier) {
        earlier.issue = 'duplicate';
        row.issue = 'duplicate';
      } else names.set(key, row);
    }
    rows.push(row);
  }
  return rows.length > 0 ? { rows } : { issue: 'empty' };
}
