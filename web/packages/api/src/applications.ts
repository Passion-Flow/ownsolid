// Applications published to a workspace, and the caller's own conversations with them, for the
// portal (api/openapi/v1/applications). A workspace the caller is not a member of fails with
// `RESOURCE_NOT_FOUND`.
import { localCell } from './cell.ts';
import { createClient } from './client.ts';
import type { applicationsContract } from './gen/index.ts';
import type { RequestOptions } from './transport.ts';

type Schemas = applicationsContract.components['schemas'];

/** A workflow published to a workspace, at its current version. */
export type Application = Schemas['Application'];

/** One page of applications, newest publication first. */
export type ApplicationPage = Schemas['ApplicationPage'];

/** One of the caller's conversations with an application. */
export type Conversation = Schemas['ApplicationConversation'];

/** One page of conversations, most recently changed first. */
export type ConversationPage = Schemas['ApplicationConversationPage'];

const client = createClient<applicationsContract.paths>();

/** Lists the applications published to a workspace. */
export function listApplications(
  workspaceId: string,
  query: { limit: number; cursor?: string | undefined },
  options?: RequestOptions,
): Promise<ApplicationPage> {
  const { limit, cursor } = query;
  return client.get('/api/v1/sites/{site_id}/cells/{cell_id}/applications', {
    ...options,
    path: localCell,
    query: { workspace_id: workspaceId, limit, ...(cursor ? { cursor } : {}) },
  });
}

/** Lists the caller's conversations in a workspace. */
export function listConversations(
  workspaceId: string,
  query: { limit: number; cursor?: string | undefined },
  options?: RequestOptions,
): Promise<ConversationPage> {
  const { limit, cursor } = query;
  return client.get('/api/v1/sites/{site_id}/cells/{cell_id}/application-conversations', {
    ...options,
    path: localCell,
    query: { workspace_id: workspaceId, limit, ...(cursor ? { cursor } : {}) },
  });
}
