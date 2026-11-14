import { keyboardFlows } from '@ownsolid/shell/testing';
import { portalPages } from './pages.ts';

keyboardFlows({ audience: 'portal', pages: portalPages });
