import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { contentSecurityPolicy } from './vite.ts';

describe('contentSecurityPolicy', () => {
  it('equals the policy the control plane sends', () => {
    const handler = new URL(
      '../../../../services/control/internal/modules/identity/http/handler.go',
      import.meta.url,
    );
    expect(readFileSync(handler, 'utf8')).toContain(`"${contentSecurityPolicy}"`);
  });
});
