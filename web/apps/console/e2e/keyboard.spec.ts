import { keyboardFlows } from '@ownsolid/shell/testing';
import { consolePages } from './pages.ts';

keyboardFlows({ audience: 'admin', pages: consolePages });
