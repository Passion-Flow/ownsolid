// What a model is, in one shape for a model that arrived as published and one made here
// (docs/research/model-library.md).
import type { DerivedModelCatalog, ModelCatalog, ModelFile, ModelVersion } from '@ownsolid/api';
import { useI18n } from '@ownsolid/i18n';
import { CopyText, Detail, DetailList, Table, Td, Th, Tr } from '@ownsolid/ui';

/** What the manifest of any model says, read once. */
export interface ModelSummary {
  repository: string;
  revision: string;
  license: string;
  format: string;
  architecture: string;
  modelType: string;
  contextTokens: number;
  files: readonly ModelFile[];
  bytes: number;
  /** The model this one was made from, when it was made here. */
  baseModelId: string | null;
}

function isDerived(manifest: ModelCatalog | DerivedModelCatalog): manifest is DerivedModelCatalog {
  return 'base_model_id' in manifest;
}

/** The size of a set of files. */
export function totalBytes(files: readonly ModelFile[]): number {
  return files.reduce((sum, file) => sum + file.size_bytes, 0);
}

/** Reads a model's manifest. */
export function modelSummary(model: ModelVersion): ModelSummary {
  const { manifest } = model;
  const derived = isDerived(manifest);
  return {
    repository: derived ? manifest.base_repository : manifest.repository,
    revision: derived ? manifest.base_revision : manifest.revision,
    license: manifest.license,
    format: manifest.format,
    architecture: manifest.architecture,
    modelType: manifest.model_type,
    contextTokens: derived ? manifest.maximum_context_tokens : manifest.max_context,
    files: manifest.files,
    bytes: totalBytes(manifest.files),
    baseModelId: derived ? manifest.base_model_id : null,
  };
}

/** The facts every surface shows about a catalogue entry or a model. */
export function ModelFacts({ summary }: { summary: Omit<ModelSummary, 'baseModelId'> }) {
  const { t, format } = useI18n();
  return (
    <DetailList>
      <Detail label={t('models.fact.repository')}>
        <span className="font-mono text-[13px]">{summary.repository}</span>
      </Detail>
      <Detail label={t('models.fact.revision')}>
        <CopyText text={summary.revision} className="break-all" />
      </Detail>
      <Detail label={t('models.fact.license')}>{summary.license}</Detail>
      <Detail label={t('models.fact.size')}>{format.bytes(summary.bytes)}</Detail>
      <Detail label={t('models.fact.context')}>
        {t('models.fact.context.tokens', { count: summary.contextTokens })}
      </Detail>
      <Detail label={t('models.fact.architecture')}>{summary.architecture}</Detail>
      <Detail label={t('models.fact.format')}>
        <span className="font-mono text-[13px]">{summary.format}</span>
      </Detail>
    </DetailList>
  );
}

/** A model's files, with the hash each must match. */
export function ModelFiles({ files }: { files: readonly ModelFile[] }) {
  const { t, format } = useI18n();
  return (
    <Table>
      <caption className="sr-only">{t('models.files')}</caption>
      <thead>
        <tr>
          <Th>{t('models.files.path')}</Th>
          <Th>{t('models.files.size')}</Th>
          <Th>{t('models.files.hash')}</Th>
        </tr>
      </thead>
      <tbody>
        {files.map((file) => (
          <Tr key={file.path}>
            <Td className="font-mono text-[13px]">{file.path}</Td>
            <Td className="whitespace-nowrap text-text-2">{format.bytes(file.size_bytes)}</Td>
            <Td>
              <CopyText text={file.sha256} className="font-mono text-[12px] break-all" />
            </Td>
          </Tr>
        ))}
      </tbody>
    </Table>
  );
}
