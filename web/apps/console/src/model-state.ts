// How models and imports are named, and the keys their reads share.
import type { ModelImport, ModelImportState, ModelVersion } from '@ownsolid/api';
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

/** The existing DTO carries retention reasons without adding a protocol state. */
export function importState(item: ModelImport): { label: TranslationKey; tone: StatusTone } {
  if (item.error_code === 'MODEL_IMPORT_EXPIRY_PENDING') {
    return { label: 'models.import.state.expiring', tone: 'warning' };
  }
  if (item.state === 'cancelled' && item.error_code === 'MODEL_IMPORT_EXPIRED') {
    return { label: 'models.import.state.expired', tone: 'warning' };
  }
  return importStates[item.state];
}

/** A retained byte count is history after cancellation or expiry, never a resumable offset. */
export function canUploadImport(item: ModelImport): boolean {
  return (
    item.state === 'uploading' &&
    !['MODEL_CANCELLATION_PENDING', 'MODEL_IMPORT_EXPIRY_PENDING', 'MODEL_IMPORT_EXPIRED'].includes(
      item.error_code ?? '',
    )
  );
}
