import { localizationFlows } from '@ownsolid/shell/testing';
import { consolePages } from './pages.ts';

localizationFlows({ audience: 'admin', pages: consolePages });
