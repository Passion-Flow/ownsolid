// @vitest-environment node
// The stylesheet must match docs/development/design-system.md, and body text must stay
// readable on every surface in both themes.
import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const css = readFileSync(new URL('./styles.css', import.meta.url), 'utf8');
const guide = readFileSync(
  new URL('../../../../docs/development/design-system.md', import.meta.url),
  'utf8',
);

function block(selector: string): Record<string, string> {
  const start = css.indexOf(`${selector} {`);
  const body = css.slice(start, css.indexOf('}', start));
  return Object.fromEntries(
    [...body.matchAll(/--([a-z0-9-]+):\s*(#[0-9a-f]{6})/g)].map((m) => [m[1], m[2]]),
  ) as Record<string, string>;
}

const light = block(':root');
const dark = { ...light, ...block(":root[data-theme='dark']") };

const guideRows: [string, string][] = [
  ['Background', 'bg'],
  ['Surface', 'surface'],
  ['Surface, secondary', 'surface-2'],
  ['Border', 'border'],
  ['Text', 'text'],
  ['Text, secondary', 'text-2'],
  ['Text, tertiary', 'text-3'],
  ['Accent (teal)', 'accent'],
];

function luminance(hex: string): number {
  const channels = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255);
  const [r = 0, g = 0, b = 0] = channels.map((c) =>
    c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4,
  );
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

function contrast(a: string, b: string): number {
  const [high, low] = [luminance(a), luminance(b)].sort((x, y) => y - x) as [number, number];
  return (high + 0.05) / (low + 0.05);
}

describe('tokens', () => {
  it.each(guideRows)('%s matches the design system guide', (row, token) => {
    expect(guide).toContain(`| ${row} | \`${light[token] ?? ''}\` | \`${dark[token] ?? ''}\` |`);
  });

  it.each([
    ['light', light],
    ['dark', dark],
  ])('body text reaches WCAG AA on every surface in the %s theme', (_name, theme) => {
    for (const surface of ['bg', 'surface', 'surface-2']) {
      for (const text of ['text', 'text-2', 'accent-ink']) {
        const ratio = contrast(theme[text] ?? '', theme[surface] ?? '');
        expect({ text, surface, passes: ratio >= 4.5 }).toEqual({ text, surface, passes: true });
      }
    }
  });
});
