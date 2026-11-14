// Automated accessibility checks: axe-core runs on every page state in both themes and
// fails the test on any WCAG 2.2 A or AA violation it can detect. Keyboard use and
// screen-reader wording still need their own flows; axe finds what is machine-checkable.
import AxeBuilder from '@axe-core/playwright';
import { expect, test, type Page } from '@playwright/test';
import { mockControlPlane, type ControlPlane } from './control-plane.ts';
import type { PageState } from './pages.ts';

/** The WCAG levels the console and the portal meet. */
export const wcagTags = ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa'];

const themes = ['light', 'dark'] as const;

/** What axe reports, one line per affected element, so a failure says what to fix. */
function findings(violations: Awaited<ReturnType<AxeBuilder['analyze']>>['violations']) {
  return violations.flatMap((violation) =>
    violation.nodes.map(
      (node) =>
        `${violation.id} (${violation.impact ?? 'unknown'}): ${node.target.join(' ')} - ${node.failureSummary ?? violation.help}`,
    ),
  );
}

/**
 * Waits until nothing is fading or moving, so a look is sampled at rest. An animation that
 * never ends, such as the mark of something running, has no rest to wait for.
 */
export async function settled(page: Page): Promise<void> {
  await page.waitForFunction(() =>
    document
      .getAnimations()
      .every(
        (animation) =>
          animation.playState !== 'running' ||
          animation.effect?.getComputedTiming().iterations === Infinity,
      ),
  );
}

/** Fails the test when axe finds a WCAG 2.2 A or AA violation on the page as it is now. */
export async function expectAccessible(page: Page): Promise<void> {
  await settled(page);
  const results = await new AxeBuilder({ page }).withTags(wcagTags).analyze();
  expect(findings(results.violations)).toEqual([]);
}

/** Options for {@link accessibilityFlows}. */
export interface AccessibilityFlowOptions {
  audience: ControlPlane['audience'];
  /** Every page state of the application, the shell's included. */
  pages: readonly PageState[];
}

/** Registers one test per page state and theme in the current spec file. */
export function accessibilityFlows({ audience, pages }: AccessibilityFlowOptions): void {
  for (const theme of themes) {
    test.describe(`accessibility, ${theme} theme`, () => {
      let control: ControlPlane;

      test.beforeEach(async ({ page }) => {
        control = await mockControlPlane(page, audience);
        await page.emulateMedia({ colorScheme: theme, reducedMotion: 'reduce' });
      });

      for (const state of pages.filter((each) => (each.audience ?? audience) === audience)) {
        test(state.name, async ({ page }) => {
          await state.open(page, control);
          await expect(page.locator('html')).toHaveAttribute('data-theme', theme);
          await expectAccessible(page);
        });
      }
    });
  }
}
