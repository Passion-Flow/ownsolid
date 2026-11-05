import { accessibilityFlows } from '@ownsolid/shell/testing';
import { portalPages } from './pages.ts';

accessibilityFlows({ audience: 'portal', pages: portalPages });
