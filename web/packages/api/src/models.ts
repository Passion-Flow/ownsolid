// Models and how they arrive, for platform administrators (api/openapi/v1/models).
import { localCell } from './cell.ts';
import { buildUrl, createClient } from './client.ts';
import type { modelsContract } from './gen/index.ts';
import { sha256Hex } from './sha256.ts';
import { writeBytes, type RequestOptions } from './transport.ts';

type Schemas = modelsContract.components['schemas'];

/** A model in a workspace. */
export type ModelVersion = Schemas['ModelVersion'];

/** What is known about a model that arrived as it was published. */
export type ModelCatalog = Schemas['ModelCatalog'];

/** What is known about a model made here from another. */
export type DerivedModelCatalog = Schemas['DerivedModelCatalog'];

/** One file of a model. */
export type ModelFile = Schemas['ModelFile'];

/** A model on its way into a workspace. */
export type ModelImport = Schemas['ModelImport'];

/** Where an import stands. */
export type ModelImportState = ModelImport['state'];

/** What this release can import, and the limits on it. */
export type ModelCatalogAvailability = Schemas['ModelCatalogAvailability'];

const client = createClient<modelsContract.paths>();

/** Reads what this release can import. */
export function getModelCatalog(options?: RequestOptions): Promise<ModelCatalogAvailability> {
  return client.get('/api/v1/sites/{site_id}/cells/{cell_id}/admin/models/catalog', {
    ...options,
    path: localCell,
  });
}

/** Lists a workspace's models, newest first. */
export async function listModels(
  workspaceId: string,
  options?: RequestOptions,
): Promise<ModelVersion[]> {
  const page = await client.get('/api/v1/sites/{site_id}/cells/{cell_id}/admin/models', {
    ...options,
    path: localCell,
    query: { workspace_id: workspaceId },
  });
  return page.items;
}

/** Lists the models of a workspace the person belongs to, for the portal. */
export async function listWorkspaceModels(
  workspaceId: string,
  options?: RequestOptions,
): Promise<ModelVersion[]> {
  const page = await client.get(
    '/api/v1/sites/{site_id}/cells/{cell_id}/workspaces/{workspace_id}/models',
    {
      ...options,
      path: { ...localCell, workspace_id: workspaceId },
    },
  );
  return page.items;
}

/** Reads one model. */
export function getModel(id: string, options?: RequestOptions): Promise<ModelVersion> {
  return client.get('/api/v1/sites/{site_id}/cells/{cell_id}/admin/models/{model_id}', {
    ...options,
    path: { ...localCell, model_id: id },
  });
}

/** Retires a model. One still in use fails with `MODEL_VERSION_REFERENCED`. */
export async function retireModel(id: string, reason: string): Promise<void> {
  await client.post('/api/v1/sites/{site_id}/cells/{cell_id}/admin/models/{model_id}/retire', {
    path: { ...localCell, model_id: id },
    body: { reason },
  });
}

/** Lists a workspace's imports, newest first. */
export async function listModelImports(
  workspaceId: string,
  options?: RequestOptions,
): Promise<ModelImport[]> {
  const page = await client.get('/api/v1/sites/{site_id}/cells/{cell_id}/admin/model-imports', {
    ...options,
    path: localCell,
    query: { workspace_id: workspaceId },
  });
  return page.items;
}

/** Reads one import. */
export function getModelImport(id: string, options?: RequestOptions): Promise<ModelImport> {
  return client.get('/api/v1/sites/{site_id}/cells/{cell_id}/admin/model-imports/{import_id}', {
    ...options,
    path: { ...localCell, import_id: id },
  });
}

/** Opens an import. The same key names the same import, so a retry cannot open two. */
export function startModelImport(
  input: {
    workspaceId: string;
    name: string;
    catalogDigest: string;
    expectedBytes: number;
    reason: string;
  },
  idempotencyKey: string,
): Promise<ModelImport> {
  return client.post('/api/v1/sites/{site_id}/cells/{cell_id}/admin/model-imports', {
    path: localCell,
    idempotencyKey,
    body: {
      workspace_id: input.workspaceId,
      name: input.name,
      catalog_digest: input.catalogDigest,
      expected_bytes: input.expectedBytes,
      license_acknowledged: true,
      reason: input.reason,
    },
  });
}

/** Stores one chunk of a bundle, which must be the next the server expects or one it holds. */
export async function uploadModelChunk(
  id: string,
  offset: number,
  chunk: Blob,
  options?: RequestOptions,
): Promise<ModelImport> {
  return writeBytes(
    buildUrl('/api/v1/sites/{site_id}/cells/{cell_id}/admin/model-imports/{import_id}/chunks', {
      ...localCell,
      import_id: id,
    }),
    chunk,
    {
      'X-Chunk-Offset': String(offset),
      'X-Chunk-SHA256': await sha256Hex(await chunk.arrayBuffer()),
    },
    options,
  );
}

/** Asks the server to verify a whole bundle, the first time or again after a failure. */
export function verifyModelImport(id: string, reason: string): Promise<ModelImport> {
  return client.post(
    '/api/v1/sites/{site_id}/cells/{cell_id}/admin/model-imports/{import_id}/complete',
    { path: { ...localCell, import_id: id }, body: { reason } },
  );
}

/** Stops an import and discards what it uploaded. */
export function cancelModelImport(id: string, reason: string): Promise<ModelImport> {
  return client.post(
    '/api/v1/sites/{site_id}/cells/{cell_id}/admin/model-imports/{import_id}/cancel',
    { path: { ...localCell, import_id: id }, body: { reason } },
  );
}
