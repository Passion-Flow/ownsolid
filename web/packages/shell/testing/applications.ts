// Scripted portal applications and conversations for browser flows.
import type { Application, Conversation, ManagedWorkspace } from '@ownsolid/api';
import type { Page, Route } from '@playwright/test';
import { defaultWorkspace } from './workspaces.ts';

/** An application published to the session's workspace. */
export const publishedApplication: Application = {
  workflow_id: '6f1c7a1e-0000-4000-8000-000000000006',
  workspace_id: defaultWorkspace.id,
  name: 'Contract reviewer',
  version_id: '6f1c7a1e-0000-4000-8000-000000000020',
  version_number: 3,
  graph_sha256: 'a'.repeat(64),
  published_at: '2026-11-03T09:00:00Z',
  can_execute: true,
  question_bytes: 4096,
};

/** One of the caller's conversations with {@link publishedApplication}. */
export const recentConversation: Conversation = {
  id: '6f1c7a1e-0000-4000-8000-000000000021',
  workspace_id: defaultWorkspace.id,
  workflow_id: publishedApplication.workflow_id,
  workflow_name: publishedApplication.name,
  version_id: publishedApplication.version_id,
  version_number: publishedApplication.version_number,
  turn_count: 4,
  created_at: '2026-11-04T07:00:00Z',
  updated_at: '2026-11-04T08:30:00Z',
  can_execute: true,
  question_bytes: 4096,
};

/** What a flow changes to script the portal's applications. */
export interface ApplicationScript {
  /** Published applications, newest first, across workspaces. */
  applications: Application[];
  /** The caller's conversations, most recently changed first, across workspaces. */
  conversations: Conversation[];
  /** Fails the application list with this problem while set. */
  applicationsProblem: { status: number; code: string } | null;
  /** Fails the conversation list with this problem while set. */
  conversationsProblem: { status: number; code: string } | null;
  /** The `workspace_id` of each application or conversation read, in order. */
  applicationReads: string[];
}

/** The state of a mock with nothing scripted. */
export function emptyApplicationScript(): ApplicationScript {
  return {
    applications: [],
    conversations: [],
    applicationsProblem: null,
    conversationsProblem: null,
    applicationReads: [],
  };
}

type Reply = (route: Route, payload: unknown, status?: number) => Promise<void>;
type Refuse = (route: Route, failure: { status: number; code: string }) => Promise<void>;

/**
 * Serves the two portal lists with the server's rules: only the portal entrance has them, and a
 * workspace the caller is not a member of is not found.
 */
export async function routeApplications(
  page: Page,
  state: ApplicationScript & { audience: string; workspaceList: ManagedWorkspace[] },
  data: Reply,
  problem: Refuse,
): Promise<void> {
  const list =
    (
      items: () => readonly { workspace_id: string }[],
      failure: () => { status: number; code: string } | null,
    ) =>
    (route: Route) => {
      const query = new URL(route.request().url()).searchParams;
      const workspace = query.get('workspace_id') ?? '';
      state.applicationReads.push(workspace);
      const member = state.workspaceList.some(
        (item) => item.id === workspace && item.role !== null,
      );
      if (state.audience !== 'portal' || !member) {
        return problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
      }
      const scripted = failure();
      if (scripted) return problem(route, scripted);
      const limit = Number(query.get('limit'));
      const matching = items().filter((item) => item.workspace_id === workspace);
      return data(route, {
        items: matching.slice(0, limit),
        next_cursor: matching.length > limit ? 'next' : null,
      });
    };
  await page.route(
    '**/api/v1/sites/default/cells/default/applications?*',
    list(
      () => state.applications,
      () => state.applicationsProblem,
    ),
  );
  await page.route(
    '**/api/v1/sites/default/cells/default/application-conversations?*',
    list(
      () => state.conversations,
      () => state.conversationsProblem,
    ),
  );
}
