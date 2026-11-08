// Scripted models and imports for browser flows. Chunks follow the server's rules, including the
// hash each must carry, so a flow proves what the page really sends.
import { createHash } from 'node:crypto';
import type { ModelCatalogAvailability, ModelImport, ModelVersion } from '@ownsolid/api';
import type { Page, Route } from '@playwright/test';

const stamp = '2026-11-06T08:00:00Z';
const workspace = '6f1c7a1e-0000-4000-8000-000000000002';
const digest = 'a'.repeat(64);

/** What this release can import, shrunk so a test bundle is a few bytes. */
export const modelCatalog = {
  catalog: {
    repository: 'Qwen/Qwen3-0.6B',
    revision: 'c1899de289a04d12100db370d81485cdf75e47ca',
    license: 'Apache-2.0',
    schema_version: 1,
    model_key: 'qwen3-0.6b',
    format: 'safetensors',
    architecture: 'Qwen3ForCausalLM',
    model_type: 'qwen3',
    max_context: 2048,
    files: [
      { path: 'config.json', size_bytes: 726, sha256: '1'.repeat(64) },
      { path: 'model.safetensors', size_bytes: 1_503_300_328, sha256: '2'.repeat(64) },
    ],
  },
  catalog_digest: digest,
  chunk_bytes: 8,
  minimum_bundle_bytes: 16,
  maximum_bundle_bytes: 64,
  maximum_attempts: 3,
  storage_available: true,
} as unknown as ModelCatalogAvailability;

/** A model ready in the workspace. */
export const readyModel: ModelVersion = {
  id: '6f1c7a1e-0000-4000-8000-000000000071',
  workspace_id: workspace,
  name: 'Qwen3-0.6B',
  model_key: 'qwen3-0.6b',
  catalog_digest: digest,
  kind: 'approved_base',
  manifest: modelCatalog.catalog,
  state: 'ready',
  created_at: stamp,
};

/** A model fine-tuned here from {@link readyModel}. */
export const derivedModel: ModelVersion = {
  ...readyModel,
  id: '6f1c7a1e-0000-4000-8000-000000000072',
  name: 'Support assistant',
  kind: 'merged_lora',
  manifest: {
    schema_version: 1,
    kind: 'merged_lora',
    scope: {
      tenant_id: readyModel.workspace_id,
      workspace_id: readyModel.workspace_id,
      preparation_id: '6f1c7a1e-0000-4000-8000-000000000078',
    },
    model_id: '6f1c7a1e-0000-4000-8000-000000000072',
    base_model_id: readyModel.id,
    attempt_id: '6f1c7a1e-0000-4000-8000-000000000073',
    artifact_id: '6f1c7a1e-0000-4000-8000-000000000074',
    base_catalog_sha256: digest,
    report_sha256: digest,
    recipe_sha256: digest,
    base_repository: 'Qwen/Qwen3-0.6B',
    base_revision: modelCatalog.catalog.revision,
    license: 'Apache-2.0',
    format: 'safetensors',
    architecture: 'Qwen3ForCausalLM',
    model_type: 'qwen3',
    precision: 'bfloat16',
    maximum_context_tokens: 2048,
    files: modelCatalog.catalog.files,
  },
} as unknown as ModelVersion;

/** An import halfway through its upload, with no browser sending it. */
export const stalledImport: ModelImport = {
  id: '6f1c7a1e-0000-4000-8000-000000000075',
  workspace_id: workspace,
  name: 'Qwen3-0.6B',
  catalog_digest: digest,
  expected_bytes: 24,
  uploaded_bytes: 8,
  state: 'uploading',
  error_code: null,
  model_id: null,
  attempts: 0,
  created_at: stamp,
  updated_at: stamp,
};

/** An import whose model has been published. The import response has no cleanup state. */
export const publishedImport: ModelImport = {
  ...stalledImport,
  id: '6f1c7a1e-0000-4000-8000-000000000077',
  uploaded_bytes: stalledImport.expected_bytes,
  model_id: readyModel.id,
  state: 'ready',
  attempts: 1,
};

/** An import whose bundle failed verification. */
export const failedImport: ModelImport = {
  ...stalledImport,
  uploaded_bytes: 24,
  state: 'failed',
  error_code: 'MODEL_DIGEST_MISMATCH',
  attempts: 1,
};

interface ScriptedFailure {
  status: number;
  code: string;
}

/** The models and imports a flow starts from, and what it makes go wrong. */
export interface ModelScript {
  models: ModelVersion[];
  modelImports: ModelImport[];
  modelCatalog: ModelCatalogAvailability;
  /** Stored chunks of each import: offset to hash. */
  modelChunks: Record<string, Record<number, string>>;
  modelsProblem: ScriptedFailure | null;
  nextModelImport: ScriptedFailure | null;
  /** Failures of the next chunks, in order; `lost` stores the chunk and drops its answer. */
  chunkFailures: (ScriptedFailure | 'lost' | 'dropped')[];
  /** Holds every chunk this long, so a flow can act during an upload. */
  chunkDelayMilliseconds: number;
  /** How verification ends: `ready`, or the code it fails with. */
  verificationOutcome: string;
  nextRetire: ScriptedFailure | null;
}

/** A script with nothing in the workspace. */
export function emptyModelScript(): ModelScript {
  return {
    models: [],
    modelImports: [],
    modelCatalog,
    modelChunks: {},
    modelsProblem: null,
    nextModelImport: null,
    chunkFailures: [],
    chunkDelayMilliseconds: 0,
    verificationOutcome: 'ready',
    nextRetire: null,
  };
}

type Reply = (route: Route, value: unknown, status?: number) => Promise<void>;
type Refuse = (route: Route, failure: ScriptedFailure) => Promise<void>;

function idOf(route: Route, fromEnd = 1): string {
  return new URL(route.request().url()).pathname.split('/').at(-fromEnd) ?? '';
}

function chunk(state: ModelScript, id: string): Record<number, string> {
  return (state.modelChunks[id] ??= {});
}

function publish(state: ModelScript, item: ModelImport): void {
  const model: ModelVersion = {
    ...readyModel,
    id: '6f1c7a1e-0000-4000-8000-000000000076',
    name: item.name,
    workspace_id: item.workspace_id,
  };
  state.models.unshift(model);
  item.state = 'ready';
  item.model_id = model.id;
  item.error_code = null;
}

/** Routes the models contract onto `state`. */
export async function routeModels(
  page: Page,
  state: ModelScript,
  data: Reply,
  problem: Refuse,
): Promise<void> {
  const base = '**/api/v1/sites/*/cells/*/admin';
  const imports = (route: Route) => state.modelImports.find((item) => item.id === idOf(route, 2));
  await page.route(`${base}/models/catalog`, (route) => data(route, state.modelCatalog));
  await page.route(`${base}/models?*`, (route) =>
    state.modelsProblem
      ? problem(route, state.modelsProblem)
      : data(route, { items: state.models }),
  );
  await page.route(/\/workspaces\/[0-9a-f-]{36}\/models$/, (route) =>
    state.modelsProblem
      ? problem(route, state.modelsProblem)
      : data(route, { items: state.models }),
  );
  await page.route(/\/admin\/models\/[0-9a-f-]{36}$/, (route) => {
    const found = state.models.find((model) => model.id === idOf(route));
    return found ? data(route, found) : problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
  });
  await page.route(/\/admin\/models\/[0-9a-f-]{36}\/retire$/, (route) => {
    if (state.nextRetire) {
      const scripted = state.nextRetire;
      state.nextRetire = null;
      return problem(route, scripted);
    }
    const found = state.models.find((model) => model.id === idOf(route, 2));
    if (!found) return problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
    state.models = state.models.filter((model) => model !== found);
    return data(route, {
      id: found.id,
      workspace_id: found.workspace_id,
      state: 'deleted',
      artifact_retention: 'immutable_files_retained',
    });
  });
  await page.route(`${base}/model-imports?*`, (route) =>
    data(route, { items: state.modelImports }),
  );
  await page.route(`${base}/model-imports`, (route) => {
    if (state.nextModelImport) {
      const scripted = state.nextModelImport;
      state.nextModelImport = null;
      return problem(route, scripted);
    }
    const body = route.request().postDataJSON() as {
      workspace_id: string;
      name: string;
      catalog_digest: string;
      expected_bytes: number;
      license_acknowledged: boolean;
      reason: string;
    };
    const key = route.request().headers()['idempotency-key'];
    if (!key || !body.license_acknowledged || body.reason === '') {
      return problem(route, { status: 400, code: 'VALIDATION_FAILED' });
    }
    const created: ModelImport = {
      ...stalledImport,
      id: '6f1c7a1e-0000-4000-8000-000000000077',
      name: body.name,
      workspace_id: body.workspace_id,
      expected_bytes: body.expected_bytes,
      uploaded_bytes: 0,
    };
    state.modelImports.unshift(created);
    return data(route, created, 201);
  });
  await page.route(/\/admin\/model-imports\/[0-9a-f-]{36}$/, (route) => {
    const found = state.modelImports.find((item) => item.id === idOf(route));
    return found ? data(route, found) : problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
  });
  await page.route(/\/admin\/model-imports\/[0-9a-f-]{36}\/chunks$/, async (route) => {
    const request = route.request();
    if (state.chunkDelayMilliseconds > 0) {
      await new Promise((resolve) => setTimeout(resolve, state.chunkDelayMilliseconds));
    }
    const failure = state.chunkFailures.shift();
    if (failure === 'dropped') return route.abort('connectionreset');
    if (failure && failure !== 'lost') return problem(route, failure);
    const found = imports(route);
    if (!found) return problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
    const headers = request.headers();
    const offset = Number(headers['x-chunk-offset']);
    const body = request.postDataBuffer() ?? Buffer.alloc(0);
    const hash = createHash('sha256').update(body).digest('hex');
    if (headers['x-chunk-sha256'] !== hash) {
      return problem(route, { status: 409, code: 'MODEL_CHUNK_INVALID' });
    }
    if (headers['content-type'] !== 'application/octet-stream') {
      return problem(route, { status: 415, code: 'UNSUPPORTED_MEDIA_TYPE' });
    }
    const stored = chunk(state, found.id);
    const recorded = stored[offset];
    if (recorded !== undefined && recorded !== hash) {
      return problem(route, { status: 409, code: 'MODEL_CHUNK_CONFLICT' });
    }
    if (recorded === undefined) {
      if (offset !== found.uploaded_bytes) {
        return problem(route, { status: 409, code: 'MODEL_CHUNK_OFFSET_MISMATCH' });
      }
      stored[offset] = hash;
      found.uploaded_bytes += body.length;
    }
    if (failure === 'lost') return route.abort('connectionreset');
    return data(route, found);
  });
  await page.route(/\/admin\/model-imports\/[0-9a-f-]{36}\/complete$/, (route) => {
    const found = imports(route);
    if (!found) return problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
    if (found.uploaded_bytes !== found.expected_bytes) {
      return problem(route, { status: 409, code: 'MODEL_IMPORT_INCOMPLETE' });
    }
    found.attempts += 1;
    if (state.verificationOutcome === 'ready') publish(state, found);
    else {
      found.state = 'failed';
      found.error_code = state.verificationOutcome;
    }
    return data(route, found, 202);
  });
  await page.route(/\/admin\/model-imports\/[0-9a-f-]{36}\/cancel$/, (route) => {
    const found = imports(route);
    if (!found) return problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
    found.state = 'cancelled';
    found.error_code = null;
    return data(route, found);
  });
}
