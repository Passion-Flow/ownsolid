import { accessibilityFlows } from '@ownsolid/shell/testing';
import { consolePages } from './pages.ts';

accessibilityFlows({ audience: 'admin', pages: consolePages });
