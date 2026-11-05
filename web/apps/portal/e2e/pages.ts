import { shellPages, type PageState } from '@ownsolid/shell/testing/pages';

/**
 * Every page state of the portal, checked by accessibility.spec.ts. A page this
 * application adds gets its states here; src/router.test.ts fails until it does.
 */
export const portalPages: readonly PageState[] = [...shellPages];
