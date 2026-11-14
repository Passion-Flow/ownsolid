import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { contentSecurityPolicy, thirdPartyNotices } from './vite.ts';

describe('contentSecurityPolicy', () => {
  it('equals the policy the control plane sends', () => {
    const handler = new URL(
      '../../../../services/control/internal/modules/identity/http/handler.go',
      import.meta.url,
    );
    expect(readFileSync(handler, 'utf8')).toContain(`"${contentSecurityPolicy}"`);
  });
});

describe('thirdPartyNotices', () => {
  it('carries the font and icon licenses', () => {
    const notices = thirdPartyNotices();
    expect(notices).toContain('@fontsource-variable/figtree');
    expect(notices).toContain('@fontsource-variable/jetbrains-mono');
    expect(notices).toContain('SIL OPEN FONT LICENSE');
    expect(notices).toContain('ISC License');
  });
});
