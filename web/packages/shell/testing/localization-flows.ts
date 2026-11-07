// Nothing reaches a page in only one language (UX-108). Every string on every page state must
// come from the catalog or from the server's data; the catalog tests already hold Chinese to the
// same keys as English, so a string that comes from the catalog exists in both. Machine
// identifiers are allowed only inside `code` and the monospaced face, where they read as what
// they are, and a bare enum value from a response does not count as data.
import english from '@ownsolid/i18n/locales/en.json' with { type: 'json' };
import chinese from '@ownsolid/i18n/locales/zh-cn.json' with { type: 'json' };
import { expect, test, type Page } from '@playwright/test';
import { settled } from './accessibility-flows.ts';
import { mockControlPlane, type ControlPlane } from './control-plane.ts';
import type { PageState } from './pages.ts';

// Both catalogs, as a state may switch language; the product's name is never translated.
const messages = [
  ...Object.values(english as Record<string, string>),
  ...Object.values(chinese as Record<string, string>),
  'ownsolid',
].map(normalize);
const exactly = new Set(messages);
const escape = (text: string) => text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
const templates = messages
  .filter((message) => /\{[a-zA-Z]/.test(message))
  .map(
    (message) =>
      new RegExp(
        `^${message
          .split(/\{[a-zA-Z][a-zA-Z0-9_]*\}/)
          .map(escape)
          .join('.+')}$`,
        's',
      ),
  );

function normalize(text: string): string {
  return text.replace(/\s+/g, ' ').trim();
}

/** What a date, time or size looks like once formatted, which no catalog holds; Intl names a size under a kilobyte `byte`. */
const formatted =
  /^[\d\s,.:/+%-]*(?:(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)[a-z]*|AM|PM|UTC|GMT|[KMGTP]i?B|B|byte|GHz|[smhd]|ms|sec|min|hr)?[\d\s,.:/+%-]*(?:AM|PM|UTC|GMT)?[\d\s,.:/+%-]*$/;

/** An enum value or an action: shown raw, it would read the same in every language. */
const machine = /^[a-z_]+(?:\.[a-z_]+)*$/;

/** The strings of a response: whole values, and every value as a word of a composed fact. */
export interface ResponseData {
  whole: Set<string>;
  words: Set<string>;
}

function collectData(value: unknown, found: ResponseData): void {
  if (typeof value === 'string') {
    found.words.add(value);
    if (!machine.test(value)) found.whole.add(normalize(value));
  } else if (Array.isArray(value)) {
    for (const child of value) collectData(child, found);
  } else if (value !== null && typeof value === 'object') {
    for (const child of Object.values(value)) collectData(child, found);
  }
}

/** Every string a person can read on the page: its text and its accessible attributes. */
function readable(page: Page): Promise<string[]> {
  return page.evaluate(() => {
    const found: string[] = [];
    const identifier = (element: Element) =>
      element.closest('code, pre, kbd, script, style, [class*="font-mono"]') !== null;
    const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      const parent = node.parentElement;
      if (parent && !identifier(parent) && parent.checkVisibility()) {
        found.push(node.textContent ?? '');
      }
    }
    for (const element of document.body.querySelectorAll(
      '[aria-label], [title], [placeholder], [alt]',
    )) {
      if (identifier(element)) continue;
      for (const name of ['aria-label', 'title', 'placeholder', 'alt']) {
        const value = element.getAttribute(name);
        if (value) found.push(value);
      }
    }
    return found;
  });
}

/** Whether a string is accounted for: a message, a value, or several values joined as a fact. */
function accounted(raw: string, data: ResponseData): boolean {
  const text = raw.replace(/^· /, '');
  if (!/[A-Za-z]{2}/.test(text) || formatted.test(text) || /^https?:\/\/\S+$/.test(text))
    return true;
  if (exactly.has(text) || data.whole.has(text)) return true;
  const words = text.split(/ · | /);
  if (words.length > 1 && words.every((word) => data.words.has(word))) return true;
  return templates.some((template) => template.test(text));
}

/** Starts gathering what the server sends, so a page's strings can be told from its data. */
export function collectResponses(page: Page, data: ResponseData): void {
  page.on('response', (response) => {
    const type = response.headers()['content-type'] ?? '';
    if (type.includes('json')) {
      void response.json().then(
        (body: unknown) => {
          collectData(body, data);
        },
        () => undefined,
      );
    }
  });
}

/** The strings on the page that neither catalog nor the server's data accounts for. */
export async function unexplainedText(page: Page, data: ResponseData): Promise<string[]> {
  await settled(page);
  return [...new Set((await readable(page)).map(normalize))]
    .filter((text) => !accounted(text, data))
    .sort();
}

/** Options for {@link localizationFlows}. */
export interface LocalizationFlowOptions {
  audience: ControlPlane['audience'];
  /** Every page state of the application, the shell's included. */
  pages: readonly PageState[];
}

/** Registers one test per page state. Help articles are text the generator already checks. */
export function localizationFlows({ audience, pages }: LocalizationFlowOptions): void {
  test.describe('every string comes from the catalog or the data', () => {
    let control: ControlPlane;
    const data: ResponseData = { whole: new Set(), words: new Set() };

    test.beforeEach(async ({ page }) => {
      data.whole.clear();
      data.words.clear();
      control = await mockControlPlane(page, audience);
      await page.emulateMedia({ reducedMotion: 'reduce' });
      collectResponses(page, data);
    });

    const walked = pages.filter(
      (each) => (each.audience ?? audience) === audience && !each.path.startsWith('/help'),
    );
    for (const state of walked) {
      test(state.name, async ({ page }) => {
        await state.open(page, control);
        expect(await unexplainedText(page, data)).toEqual([]);
      });
    }
  });
}
