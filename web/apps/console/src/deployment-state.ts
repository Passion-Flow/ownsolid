// How a deployment's state reads, which tile it counts under, and the keys its reads share
// (docs/research/model-services.md).
import type { Deployment } from '@ownsolid/api';
import type { TranslationKey } from '@ownsolid/i18n';
import type { StatusTone } from '@ownsolid/ui';

/** Everything under this key follows a change to deployments. */
export const deploymentsKey = ['deployments'] as const;

/** The keys of the reads under {@link deploymentsKey}. */
export const deploymentKeys = {
  list: (workspace: string, model?: string) =>
    [...deploymentsKey, 'list', workspace, model ?? null] as const,
  deployment: (id: string) => [...deploymentsKey, 'deployment', id] as const,
  placement: (model: string) => [...deploymentsKey, 'placement', model] as const,
  callers: (workspace: string, deployment: string) =>
    [...deploymentsKey, 'callers', workspace, deployment] as const,
};

/** The four groups the list's tiles filter by. */
export const phases = ['healthy', 'degraded', 'changing', 'stopped'] as const;

/** One of {@link phases}. */
export type Phase = (typeof phases)[number];

/** The word a deployment's state reads as. */
type Status =
  | 'removed'
  | 'running'
  | 'starting'
  | 'stopping'
  | 'removing'
  | 'stopped'
  | 'failed'
  | 'notReporting'
  | 'recovery'
  | 'unknown';

/** What a deployment's state means to the person: its group, its word and its tone. */
export interface DeploymentStatus {
  phase: Phase;
  label: TranslationKey;
  tone: StatusTone;
  /** The code that explains a failure, when there is one. */
  error: string | null;
}

const tones: Record<Status, StatusTone> = {
  removed: 'neutral',
  running: 'good',
  starting: 'live',
  stopping: 'live',
  removing: 'live',
  stopped: 'neutral',
  failed: 'critical',
  notReporting: 'warning',
  recovery: 'critical',
  unknown: 'warning',
};

function status(phase: Phase, word: Status, error: string | null = null): DeploymentStatus {
  return { phase, label: `services.status.${word}`, tone: tones[word], error };
}

/**
 * Reads a deployment's state. Work in progress comes first, as what it reports is about to change;
 * a GPU that needs recovery comes next, as nothing else can happen until it is resolved.
 */
export function deploymentStatus(d: Deployment): DeploymentStatus {
  if (d.removed_at !== null) return status('stopped', 'removed');
  // An observation with nothing wrong carries an empty code, which reads as no failure.
  const error = d.task_error_code ?? d.observation?.error_code ?? null;
  const working = d.task_state === 'pending' || d.task_state === 'running';
  if (working || d.observed_state === 'loading') {
    if (d.desired_state === 'removed') return status('changing', 'removing');
    return status('changing', d.desired_state === 'stopped' ? 'stopping' : 'starting');
  }
  if (d.resource_state === 'recovery_required') return status('degraded', 'recovery', error);
  const halted = d.observed_state === 'stopped' || d.observed_state === 'not_installed';
  if (d.desired_state !== 'running' && halted && !d.stale) return status('stopped', 'stopped');
  if (d.stale) return status('degraded', 'notReporting', error);
  if (d.observed_state === 'running' && d.observation?.ready === true) {
    return status('healthy', 'running');
  }
  if (d.observed_state === 'failed' || d.task_state === 'failed') {
    return status('degraded', 'failed', error);
  }
  return status('degraded', 'unknown', error);
}

/** Whether a deployment can be started: it is not running, not changing and not stuck. */
export function canStart(d: Deployment): boolean {
  if (d.removed_at !== null) return false;
  const s = deploymentStatus(d);
  return (
    s.phase !== 'changing' && s.phase !== 'healthy' && d.resource_state !== 'recovery_required'
  );
}

/** Whether a deployment can be stopped: it means to run and nothing is changing it. */
export function canStop(d: Deployment): boolean {
  return d.desired_state === 'running' && deploymentStatus(d).phase !== 'changing';
}
