import { describe, expect, it } from 'vitest';
import { connectorCallbackUrl } from './sso.ts';

describe('connectorCallbackUrl', () => {
  it('is the path the provider returns to, at an entrance origin', () => {
    expect(
      connectorCallbackUrl(
        'https://admin.example.test:8443',
        '6f1c7a1e-0000-4000-8000-000000000301',
      ),
    ).toBe(
      'https://admin.example.test:8443/api/v1/auth/sso/6f1c7a1e-0000-4000-8000-000000000301/callback',
    );
  });
});
