import { shellPages, type PageState } from '@ownsolid/shell/testing/pages';

/**
 * Every page state of the console, checked by accessibility.spec.ts. A page this
 * application adds gets its states here; src/router.test.ts fails until it does.
 */
export const consolePages: readonly PageState[] = [...shellPages];
