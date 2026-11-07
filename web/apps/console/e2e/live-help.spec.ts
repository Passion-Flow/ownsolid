import { liveHelpFlows } from '@ownsolid/shell/testing';
import { liveOptions, origin } from '@ownsolid/shell/testing/live';
import { test } from '@playwright/test';

test.skip(origin === '', 'needs an installation');
test.use(liveOptions);

liveHelpFlows('admin');
