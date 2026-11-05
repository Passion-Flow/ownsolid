// Keyboard and screen-reader flows every application must pass: each control on every
// page state is reachable with Tab and shows where focus is, a new page takes focus and
// names itself, and layers close with Escape and give focus back.
import { expect, test, type Page } from '@playwright/test';
import { account, mockControlPlane, type ControlPlane } from './control-plane.ts';
import type { PageState } from './pages.ts';

/** One element Tab stopped on. */
interface TabStop {
  /** Tag and accessible name, to say which control failed. */
  label: string;
  /** Inside the viewport once focused, with a size. */
  visible: boolean;
  /** Looks different focused than unfocused: outline, ring, border or background. */
  indicated: boolean;
}

// More stops than any page has; reaching it means focus is stuck in a loop it cannot leave.
const maximumStops = 120;

/** Waits until no transition or animation runs, so a look is sampled once it has settled. */
async function settled(page: Page): Promise<void> {
  await page.waitForFunction(() =>
    document.getAnimations().every((animation) => animation.playState !== 'running'),
  );
}

/**
 * Presses Tab until focus returns to a control it has visited. Each stop's look is
 * recorded while focused and compared with its look once focus has left the page.
 */
async function walkTabOrder(page: Page): Promise<TabStop[]> {
  for (let index = 1; index <= maximumStops; index += 1) {
    await page.keyboard.press('Tab');
    await settled(page);
    const done = await page.evaluate((visit) => {
      const element = document.activeElement;
      // Focus is on the browser's own controls between two passes through the page.
      if (!(element instanceof HTMLElement) || element === document.body) return false;
      if (element.dataset.tabVisit) return true;
      const style = getComputedStyle(element);
      const rect = element.getBoundingClientRect();
      element.dataset.tabVisit = String(visit);
      element.dataset.tabLook = [
        style.outlineStyle,
        style.outlineWidth,
        style.outlineColor,
        style.boxShadow,
        style.borderColor,
        style.backgroundColor,
      ].join('|');
      element.dataset.tabVisible = String(
        rect.width > 0 &&
          rect.height > 0 &&
          rect.bottom > 0 &&
          rect.right > 0 &&
          rect.top < window.innerHeight &&
          rect.left < window.innerWidth,
      );
      return false;
    }, index);
    if (done) break;
    if (index === maximumStops) throw new Error(`Tab did not cycle within ${maximumStops} stops`);
  }
  await page.evaluate(() => {
    (document.activeElement as HTMLElement | null)?.blur();
  });
  await settled(page);
  return page.evaluate(() =>
    [...document.querySelectorAll<HTMLElement>('[data-tab-visit]')]
      .sort((a, b) => Number(a.dataset.tabVisit) - Number(b.dataset.tabVisit))
      .map((element) => {
        const style = getComputedStyle(element);
        const look = [
          style.outlineStyle,
          style.outlineWidth,
          style.outlineColor,
          style.boxShadow,
          style.borderColor,
          style.backgroundColor,
        ].join('|');
        const name =
          element.getAttribute('aria-label') ??
          (element.textContent.trim().slice(0, 40) ||
            (element.getAttribute('placeholder') ?? element.id));
        return {
          label: `${element.tagName.toLowerCase()} "${name}"`,
          visible: element.dataset.tabVisible === 'true',
          indicated: look !== element.dataset.tabLook,
        };
      }),
  );
}

/** Fails the test unless Tab reaches at least one control and each one shows focus. */
export async function expectFocusVisible(page: Page): Promise<void> {
  const stops = await walkTabOrder(page);
  expect(stops.length).toBeGreaterThan(0);
  expect(stops.filter((stop) => !stop.visible || !stop.indicated)).toEqual([]);
}

/** True when focus is in an open menu, which moves with the arrow keys instead of Tab. */
function inMenu(page: Page): Promise<boolean> {
  return page.evaluate(() => Boolean(document.activeElement?.closest('[role="menu"]')));
}

/** Options for {@link keyboardFlows}. */
export interface KeyboardFlowOptions {
  audience: ControlPlane['audience'];
  /** Every page state of the application, the shell's included. */
  pages: readonly PageState[];
}

/** Registers the keyboard flows in the current spec file. */
export function keyboardFlows({ audience, pages }: KeyboardFlowOptions): void {
  test.describe('keyboard', () => {
    let control: ControlPlane;

    test.beforeEach(async ({ page }) => {
      control = await mockControlPlane(page, audience);
      await page.emulateMedia({ reducedMotion: 'reduce' });
    });

    for (const state of pages.filter((each) => (each.audience ?? audience) === audience)) {
      test(`${state.name}: every control is reachable and shows focus`, async ({ page }) => {
        await state.open(page, control);
        if (await inMenu(page)) {
          // Each item takes focus in turn and is highlighted while it has it.
          const items = page.getByRole('menuitem');
          for (let index = 0; index < (await items.count()); index += 1) {
            await page.keyboard.press('ArrowDown');
            await expect(items.nth(index)).toBeFocused();
            await expect(items.nth(index)).toHaveAttribute('data-highlighted', '');
          }
          return;
        }
        await expectFocusVisible(page);
      });
    }

    test('sign-in follows the order it is read in', async ({ page }) => {
      await page.goto('/sign-in');
      await page.getByLabel('Email').waitFor();
      const order: string[] = [];
      for (let index = 0; index < 5; index += 1) {
        await page.keyboard.press('Tab');
        order.push(
          await page.evaluate(() => {
            const element = document.activeElement;
            return element?.getAttribute('aria-label') ?? element?.id ?? '';
          }),
        );
      }
      await expect(page.getByLabel('Email')).toHaveCount(1);
      const email = await page.getByLabel('Email').getAttribute('id');
      const password = await page.getByLabel('Password').getAttribute('id');
      expect(order.slice(0, 2)).toEqual([email, password]);
    });

    test('moves focus to the heading of the next page and names it', async ({ page }) => {
      await page.goto('/sign-in');
      await expect(page).toHaveTitle('Sign in - ownsolid');
      await page.getByRole('link', { name: 'Forgot your password?' }).focus();
      await page.keyboard.press('Enter');
      const heading = page.getByRole('heading', { name: 'Reset your password', level: 1 });
      await expect(heading).toBeFocused();
      await expect(page).toHaveTitle('Reset your password - ownsolid');
      await page.keyboard.press('Tab');
      await expect(page.getByLabel('Email')).toBeFocused();
    });

    test('keeps focus on the first load of a page', async ({ page }) => {
      await page.goto('/sign-in');
      await page.getByLabel('Email').waitFor();
      expect(await page.evaluate(() => document.activeElement === document.body)).toBe(true);
    });

    test('names status pages and gives them a heading', async ({ page }) => {
      await page.goto('/no/such/page');
      await expect(page.getByRole('heading', { name: 'Page not found', level: 1 })).toBeVisible();
      await expect(page).toHaveTitle('Page not found - ownsolid');
    });

    test('skips to the main content', async ({ page }) => {
      control.signedIn = true;
      await page.goto('/');
      await page.getByRole('heading', { name: 'Welcome, Ada Lovelace' }).waitFor();
      await page.keyboard.press('Tab');
      const skip = page.getByRole('link', { name: 'Skip to main content' });
      await expect(skip).toBeFocused();
      await expect(skip).toBeInViewport();
      await page.keyboard.press('Enter');
      await expect(page.locator('#main')).toBeFocused();
    });

    test('closes the account menu with Escape and returns focus', async ({ page }) => {
      control.signedIn = true;
      await page.goto('/');
      const button = page.getByRole('button', { name: 'Account menu' });
      await button.focus();
      await page.keyboard.press('Enter');
      await expect(page.getByRole('menuitem').first()).toBeFocused();
      await page.keyboard.press('Escape');
      await expect(page.getByRole('menu')).toBeHidden();
      await expect(button).toBeFocused();
    });

    test('closes the notifications drawer with Escape and returns focus', async ({ page }) => {
      control.signedIn = true;
      await page.goto('/');
      const bell = page.getByRole('button', { name: /^Notifications/ });
      await bell.focus();
      await page.keyboard.press('Enter');
      await expect(page.getByRole('dialog', { name: 'Notifications' })).toBeVisible();
      await page.keyboard.press('Escape');
      await expect(page.getByRole('dialog', { name: 'Notifications' })).toBeHidden();
      await expect(bell).toBeFocused();
    });

    test('closes the command menu with Escape and returns focus', async ({ page }) => {
      control.signedIn = true;
      await page.goto('/');
      const search = page.getByRole('button', { name: 'Search, or type a command' });
      await search.focus();
      await page.keyboard.press('Enter');
      await expect(page.getByPlaceholder('Type a command or a page name')).toBeFocused();
      await page.keyboard.press('Escape');
      await expect(search).toBeFocused();
    });

    test('runs sign-in from the keyboard alone', async ({ page }) => {
      await page.goto('/sign-in');
      await page.getByLabel('Email').waitFor();
      await page.keyboard.press('Tab');
      await page.keyboard.type(account.email);
      await page.keyboard.press('Tab');
      await page.keyboard.type(account.password);
      await page.keyboard.press('Enter');
      const heading = page.getByRole('heading', { name: 'Welcome, Ada Lovelace', level: 1 });
      await expect(heading).toBeFocused();
    });
  });
}
