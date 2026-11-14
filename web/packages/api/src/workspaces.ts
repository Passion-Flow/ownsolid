// Workspaces and their members, for platform administrators (api/openapi/v1/workspaces).
import { createClient } from './client.ts';
import type { workspacesContract } from './gen/index.ts';
import type { RequestOptions } from './transport.ts';

type Schemas = workspacesContract.components['schemas'];

/** A workspace. */
export type Workspace = Schemas['Workspace'];

/** One page of workspaces. */
export type WorkspaceListPage = Schemas['WorkspaceListResponse']['data'];

/** A person's place in a workspace. */
export type Membership = Schemas['Membership'];

/** What a member may do. */
export type MemberRole = Membership['role'];

/** One page of members. */
export type MembershipPage = Schemas['MembershipListResponse']['data'];

const client = createClient<workspacesContract.paths>();

/** Lists every workspace, oldest first. */
export function listAllWorkspaces(
  query: {
    limit: number;
    cursor?: string | undefined;
    /** Text anywhere in the name, in any case. */
    search?: string | undefined;
  },
  options?: RequestOptions,
): Promise<WorkspaceListPage> {
  const { limit, cursor, search } = query;
  return client.get('/api/v1/admin/workspaces', {
    ...options,
    query: { limit, ...(cursor ? { cursor } : {}), ...(search ? { q: search } : {}) },
  });
}

/** Reads one workspace. */
export function getWorkspace(id: string, options?: RequestOptions): Promise<Workspace> {
  return client.get('/api/v1/admin/workspaces/{workspace_id}', {
    ...options,
    path: { workspace_id: id },
  });
}

/** Creates a workspace owned by an account. A name in use fails with `WORKSPACE_EXISTS`. */
export function createWorkspace(input: { name: string; ownerUserId: string }): Promise<Workspace> {
  return client.post('/api/v1/admin/workspaces', {
    body: { name: input.name, owner_user_id: input.ownerUserId },
  });
}

/** Renames, disables or enables a workspace at the version it was read at. */
export function updateWorkspace(
  id: string,
  change: { name: string; status: Workspace['status']; version: number; reason: string },
): Promise<Workspace> {
  return client.patch('/api/v1/admin/workspaces/{workspace_id}', {
    path: { workspace_id: id },
    body: change,
  });
}

/** Lists a workspace's members. */
export function listMembers(
  id: string,
  query: { limit: number; cursor?: string | undefined },
  options?: RequestOptions,
): Promise<MembershipPage> {
  const { limit, cursor } = query;
  return client.get('/api/v1/admin/workspaces/{workspace_id}/members', {
    ...options,
    path: { workspace_id: id },
    query: { limit, ...(cursor ? { cursor } : {}) },
  });
}

/** Adds the account with this address. Fails with `RESOURCE_NOT_FOUND` when there is none. */
export function addMember(
  id: string,
  input: { email: string; role: MemberRole },
): Promise<Membership> {
  return client.post('/api/v1/admin/workspaces/{workspace_id}/members', {
    path: { workspace_id: id },
    body: input,
  });
}

/** Changes a member's role at the version it was read at. */
export function changeMember(
  id: string,
  user: string,
  change: { role: MemberRole; version: number; reason: string },
): Promise<Membership> {
  return client.patch('/api/v1/admin/workspaces/{workspace_id}/members/{user_id}', {
    path: { workspace_id: id, user_id: user },
    body: change,
  });
}

/** Removes a member. The last owner cannot be removed (`LAST_WORKSPACE_OWNER`). */
export async function removeMember(
  id: string,
  user: string,
  change: { version: number; reason: string },
): Promise<void> {
  await client.delete('/api/v1/admin/workspaces/{workspace_id}/members/{user_id}', {
    path: { workspace_id: id, user_id: user },
    body: change,
  });
}
