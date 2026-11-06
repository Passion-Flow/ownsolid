// What the console calls a task's operation, state and steps.
import type { TaskOperation, TaskState, TaskStepName } from '@ownsolid/api';
import type { TranslationKey } from '@ownsolid/i18n';
import type { StatusTone } from '@ownsolid/ui';

/** The name of each operation. */
export const operations: Record<TaskOperation, TranslationKey> = {
  'collect_facts.v1': 'tasks.operation.collect_facts',
  'node_exporter.install.v1': 'tasks.operation.exporter_install',
  'node_exporter.start.v1': 'tasks.operation.exporter_start',
  'node_exporter.stop.v1': 'tasks.operation.exporter_stop',
  'node_exporter.remove.v1': 'tasks.operation.exporter_remove',
  'node_exporter.observe.v1': 'tasks.operation.exporter_observe',
  'node_exporter.configure.v1': 'tasks.operation.exporter_configure',
  'node_exporter.restart.v1': 'tasks.operation.exporter_restart',
  'node_exporter.upgrade.v1': 'tasks.operation.exporter_upgrade',
  'node_exporter.rollback.v1': 'tasks.operation.exporter_rollback',
  'node_exporter.logs.v1': 'tasks.operation.exporter_logs',
  'inference.deploy.v1': 'tasks.operation.inference_deploy',
  'inference.stop.v1': 'tasks.operation.inference_stop',
  'inference.remove.v1': 'tasks.operation.inference_remove',
  'inference.observe.v1': 'tasks.operation.inference_observe',
  'inference.verify.v1': 'tasks.operation.inference_verify',
  'inference.derived.v1': 'tasks.operation.inference_derived',
  'training.run.v1': 'tasks.operation.training_run',
  'model.prepare.v1': 'tasks.operation.model_prepare',
};

/** The tone and name of each state. */
export const states: Record<TaskState, { tone: StatusTone; label: TranslationKey }> = {
  pending: { tone: 'neutral', label: 'tasks.state.pending' },
  running: { tone: 'live', label: 'tasks.state.running' },
  succeeded: { tone: 'good', label: 'tasks.state.succeeded' },
  failed: { tone: 'critical', label: 'tasks.state.failed' },
  cancelled: { tone: 'neutral', label: 'tasks.state.cancelled' },
};

/** The name of each step. */
export const stepNames: Record<TaskStepName, TranslationKey> = {
  read_system: 'tasks.step.read_system',
  read_memory: 'tasks.step.read_memory',
  read_disk: 'tasks.step.read_disk',
  preflight: 'tasks.step.preflight',
  download: 'tasks.step.download',
  install: 'tasks.step.install',
  start: 'tasks.step.start',
  stop: 'tasks.step.stop',
  remove: 'tasks.step.remove',
  verify: 'tasks.step.verify',
};

/** The path of one task's page. */
export function taskPath(id: string): string {
  return `/tasks/${id}`;
}
