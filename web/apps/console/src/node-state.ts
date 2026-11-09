// What the console calls a node's state. What an administrator chose comes first: a disabled node
// that still reports is disabled.
import type { Node } from '@ownsolid/api';
import type { TranslationKey } from '@ownsolid/i18n';
import type { StatusTone } from '@ownsolid/ui';

/** The tone and name of a node's state. */
export function nodeState(node: Node): { tone: StatusTone; label: TranslationKey } {
  if (node.status === 'removed') return { tone: 'neutral', label: 'nodes.state.removed' };
  if (node.status === 'disabled') return { tone: 'neutral', label: 'nodes.state.disabled' };
  switch (node.connectivity) {
    case 'online':
      return { tone: 'good', label: 'nodes.state.online' };
    case 'unknown':
      return { tone: 'warning', label: 'nodes.state.unknown' };
    case 'offline':
      return { tone: 'critical', label: 'nodes.state.offline' };
  }
}

/** The name of each execution profile. */
export const profiles = {
  read_only: 'nodes.profile.read_only',
  systemd_components: 'nodes.profile.systemd_components',
  systemd_maintenance: 'nodes.profile.systemd_maintenance',
  docker_inference: 'nodes.profile.docker_inference',
  docker_ai: 'nodes.profile.docker_ai',
  docker_model_lifecycle: 'nodes.profile.docker_model_lifecycle',
  kubernetes_node: 'nodes.profile.kubernetes_node',
} satisfies Record<Node['execution_profile'] | 'kubernetes_node', TranslationKey>;
