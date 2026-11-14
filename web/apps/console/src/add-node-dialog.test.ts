import { readFileSync } from 'node:fs';
import { join } from 'node:path';
import { describe, expect, it } from 'vitest';
import { imageCommands, servingImage } from './add-node-dialog.tsx';

const recipe = JSON.parse(
  readFileSync(
    join(
      import.meta.dirname,
      '..',
      '..',
      '..',
      '..',
      'api',
      'agent',
      'v1',
      'inference-recipe.json',
    ),
    'utf8',
  ),
) as { image_repository: string; image_manifest_digest: string };

describe('the serving image a node is told to load', () => {
  it('is the one the approved recipe runs', () => {
    expect(servingImage).toBe(`${recipe.image_repository}@${recipe.image_manifest_digest}`);
    expect(imageCommands).toContain(`docker pull ${servingImage}`);
  });
});
