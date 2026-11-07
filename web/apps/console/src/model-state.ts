// How models and imports are named, and the keys their reads share.
import type { ModelImportState, ModelVersion } from '@ownsolid/api';
import type { TranslationKey } from '@ownsolid/i18n';
import type { StatusTone } from '@ownsolid/ui';

/** Everything under this key follows a change to models or imports. */
export const modelsKey = ['models'] as const;

/** The keys of the reads under {@link modelsKey}. */
export const modelKeys = {
  catalog: [...modelsKey, 'catalog'] as const,
  list: (workspace: string) => ({ queryKey: [...modelsKey, 'list', workspace] as const }),
  imports: (workspace: string) => ({ queryKey: [...modelsKey, 'imports', workspace] as const }),
  model: (id: string) => [...modelsKey, 'model', id] as const,
  import: (id: string) => [...modelsKey, 'import', id] as const,
};

/** The name of each kind of model. */
export const modelKinds: Record<ModelVersion['kind'], TranslationKey> = {
  approved_base: 'models.kind.approved_base',
  merged_lora: 'models.kind.merged_lora',
};

/** How each state of an import reads. */
export const importStates: Record<ModelImportState, { label: TranslationKey; tone: StatusTone }> = {
  uploading: { label: 'models.import.state.uploading', tone: 'neutral' },
  queued: { label: 'models.import.state.queued', tone: 'neutral' },
  verifying: { label: 'models.import.state.verifying', tone: 'neutral' },
  ready: { label: 'models.import.state.ready', tone: 'good' },
  failed: { label: 'models.import.state.failed', tone: 'critical' },
  cancelled: { label: 'models.import.state.cancelled', tone: 'neutral' },
};
