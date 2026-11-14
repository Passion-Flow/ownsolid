import { Server } from 'lucide-react';
import { describe, expect, it } from 'vitest';
import { findNavItem, type NavGroup } from './navigation.ts';

const overview = { label: 'nav.overview', icon: Server, to: '/' } as const;
const nodes = { label: 'nav.nodes', icon: Server, to: '/nodes' } as const;
const clusters = { label: 'nav.clusters', icon: Server } as const;
const groups: NavGroup[] = [
  { items: [overview] },
  { heading: 'nav.group.infrastructure', items: [clusters, nodes] },
];

describe('findNavItem', () => {
  it('matches the start page only exactly', () => {
    expect(findNavItem(groups, '/')?.item).toBe(overview);
    expect(findNavItem(groups, '/other')).toBeUndefined();
  });

  it('matches a page and the paths below it, with its group', () => {
    expect(findNavItem(groups, '/nodes')?.item).toBe(nodes);
    const found = findNavItem(groups, '/nodes/gpu-a01');
    expect(found?.item).toBe(nodes);
    expect(found?.group.heading).toBe('nav.group.infrastructure');
  });

  it('does not match a path that only shares a prefix', () => {
    expect(findNavItem(groups, '/nodes-archive')).toBeUndefined();
  });
});
