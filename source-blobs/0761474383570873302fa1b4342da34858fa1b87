import type { CapabilityDeclaration, CapabilityOption } from '@ownsolid/api';
import { describe, expect, it } from 'vitest';
import {
  capabilityDetail,
  capabilityName,
  optionMatches,
  readyCapabilityNames,
} from './capabilities.ts';

const t = (key: string) => `text of ${key}`;
const registry = { id: 'accelerators' };
const ready: CapabilityOption = { id: 'cpu', name: 'CPU only', state: 'ready', note: 'Fact.' };
const planned: CapabilityOption = {
  id: 'nvidia',
  name: 'NVIDIA',
  state: 'planned',
  roadmap_item: 'GPU-101',
  reason: 'Why.',
};
const declaration: CapabilityDeclaration = {
  registries: [{ id: 'accelerators', name: 'A', description: 'B', options: [ready, planned] }],
};

describe('capability text', () => {
  it('finds the translated name, reason and note', () => {
    expect(capabilityName(t, registry, planned)).toBe(
      'text of capability.accelerators.nvidia.name',
    );
    expect(capabilityDetail(t, registry, planned)).toBe(
      'text of capability.accelerators.nvidia.reason',
    );
    expect(capabilityDetail(t, registry, ready)).toBe('text of capability.accelerators.cpu.note');
  });

  it('has no detail for an option without one', () => {
    expect(capabilityDetail(t, registry, { id: 'x', name: 'X', state: 'ready' })).toBe('');
  });

  it('names only what can be chosen', () => {
    expect(readyCapabilityNames(t, declaration, 'accelerators')).toEqual([
      'text of capability.accelerators.cpu.name',
    ]);
  });

  it('names nothing before the declaration arrives or for an unknown registry', () => {
    expect(readyCapabilityNames(t, undefined, 'accelerators')).toEqual([]);
    expect(readyCapabilityNames(t, declaration, 'nothing')).toEqual([]);
  });
});

describe('optionMatches', () => {
  it('finds everything with no search', () => {
    expect(optionMatches('  ', planned, [])).toBe(true);
  });

  it('finds by English name, roadmap item and what is shown, in any case', () => {
    expect(optionMatches('nvid', planned, [])).toBe(true);
    expect(optionMatches('gpu-101', planned, [])).toBe(true);
    expect(optionMatches('\u82f1\u4f1f\u8fbe', planned, ['\u82f1\u4f1f\u8fbe GPU'])).toBe(true);
  });

  it('leaves out what does not match', () => {
    expect(optionMatches('ascend', planned, ['Why.'])).toBe(false);
  });
});
