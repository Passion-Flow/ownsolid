// Scripted workspaces and members for browser flows.
import type { ManagedWorkspace as Workspace, Membership } from '@ownsolid/api';
import type { Page, Route } from '@playwright/test';

const stamp = '2026-11-04T00:00:00Z';

/** The workspace the mock's session works in. */
export const defaultWorkspace: Workspace = {
  id: '6f1c7a1e-0000-4000-8000-000000000002',
  tenant_id: '6f1c7a1e-0000-4000-8000-000000000003',
  name: 'Research Lab',
  status: 'active',
  version: 1,
  is_default: true,
  role: 'owner',
  created_at: stamp,
  updated_at: stamp,
};

/** A workspace an administrator can disable. */
export const teamWorkspace: Workspace = {
  ...defaultWorkspace,
  id: '6f1c7a1e-0000-4000-8000-000000000004',
  name: 'Platform Team',
  version: 3,
  is_default: false,
  role: null,
};

/** An owner of a workspace. */
export const ownerMember: Membership = {
  user_id: '6f1c7a1e-0000-4000-8000-000000000001',
  email: 'ada@example.test',
  display_name: 'Ada Lovelace',
  user_status: 'active',
  role: 'owner',
  version: 1,
  created_at: stamp,
  updated_at: stamp,
};

/** A member of a workspace. */
export const plainMember: Membership = {
  ...ownerMember,
  user_id: '6f1c7a1e-0000-4000-8000-000000000070',
  email: 'grace@example.test',
  display_name: 'Grace Hopper',
  role: 'member',
  version: 2,
};

/** What a flow changes to script workspaces. */
export interface WorkspaceScript {
  workspaceList: Workspace[];
  /** Members by workspace ID. */
  members: Record<string, Membership[]>;
  membersProblem: { status: number; code: string } | null;
  /** The next write to a workspace or a member fails like this. */
  nextWorkspaceChange: { status: number; code: string } | null;
  /** Reasons given for changes, in order. */
  workspaceReasons: string[];
}

/** The state of a mock with nothing scripted. */
export function emptyWorkspaceScript(): WorkspaceScript {
  return {
    workspaceList: [defaultWorkspace, teamWorkspace],
    members: {
      [defaultWorkspace.id]: [ownerMember],
      [teamWorkspace.id]: [ownerMember, plainMember],
    },
    membersProblem: null,
    nextWorkspaceChange: null,
    workspaceReasons: [],
  };
}

type Reply = (route: Route, payload: unknown, status?: number) => Promise<void>;
type Refuse = (route: Route, failure: { status: number; code: string }) => Promise<void>;

/** Serves one workspace and its members from `state`, with the server's rules. */
export async function routeWorkspaces(
  page: Page,
  state: WorkspaceScript & { audience: string },
  data: Reply,
  problem: Refuse,
): Promise<void> {
  const parts = (route: Route) => new URL(route.request().url()).pathname.split('/');
  // Writes pass here first; a scripted failure or a refused token stops them.
  const refused = (route: Route) => {
    if (state.audience !== 'admin') return { status: 404, code: 'RESOURCE_NOT_FOUND' };
    if (route.request().headers()['x-csrf-token'] !== 'csrf-token') {
      return { status: 403, code: 'CSRF_INVALID' };
    }
    const scripted = state.nextWorkspaceChange;
    state.nextWorkspaceChange = null;
    return scripted;
  };
  await page.route('**/api/v1/admin/workspaces', (route) => {
    const failure = refused(route);
    if (failure) return problem(route, failure);
    const body = route.request().postDataJSON() as { name: string };
    if (state.workspaceList.some((item) => item.name === body.name)) {
      return problem(route, { status: 409, code: 'WORKSPACE_EXISTS' });
    }
    const created: Workspace = {
      ...teamWorkspace,
      id: `6f1c7a1e-0000-4000-8000-0000000001${String(state.workspaceList.length).padStart(2, '0')}`,
      name: body.name,
      version: 1,
    };
    state.workspaceList.push(created);
    state.members[created.id] = [ownerMember];
    return data(route, created, 201);
  });
  await page.route(/\/api\/v1\/admin\/workspaces\/[0-9a-f-]{36}$/, (route) => {
    const id = parts(route).at(-1) ?? '';
    const found = state.workspaceList.find((item) => item.id === id);
    const request = route.request();
    if (request.method() === 'GET') {
      if (state.audience !== 'admin' || !found) {
        return problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
      }
      return data(route, found);
    }
    const failure = refused(route);
    if (failure) return problem(route, failure);
    if (!found) return problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
    const body = request.postDataJSON() as {
      name: string;
      status: Workspace['status'];
      version: number;
      reason: string;
    };
    if (body.version !== found.version) {
      return problem(route, { status: 409, code: 'VERSION_CONFLICT' });
    }
    state.workspaceReasons.push(body.reason);
    found.name = body.name;
    found.status = body.status;
    found.version += 1;
    return data(route, found);
  });
  await page.route(/\/api\/v1\/admin\/workspaces\/[0-9a-f-]{36}\/members(\?.*)?$/, (route) => {
    const id = parts(route).at(-2) ?? '';
    const members = state.members[id] ?? [];
    const request = route.request();
    if (request.method() === 'GET') {
      if (state.audience !== 'admin') {
        return problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
      }
      if (state.membersProblem) return problem(route, state.membersProblem);
      return data(route, { items: members, next_cursor: null });
    }
    const failure = refused(route);
    if (failure) return problem(route, failure);
    const body = request.postDataJSON() as { email: string; role: Membership['role'] };
    if (members.some((member) => member.email === body.email)) {
      return problem(route, { status: 409, code: 'MEMBERSHIP_EXISTS' });
    }
    if (body.email.endsWith('@nowhere.test')) {
      return problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
    }
    const known = Object.values(state.members)
      .flat()
      .find((member) => member.email === body.email);
    const added: Membership = {
      ...(known ?? plainMember),
      user_id: known?.user_id ?? '6f1c7a1e-0000-4000-8000-0000000000b1',
      email: body.email,
      role: body.role,
      version: 1,
    };
    state.members[id] = [...members, added];
    return data(route, added, 201);
  });
  await page.route(
    /\/api\/v1\/admin\/workspaces\/[0-9a-f-]{36}\/members\/[0-9a-f-]{36}$/,
    (route) => {
      const [, , , , , id = '', , user = ''] = parts(route);
      const members = state.members[id] ?? [];
      const found = members.find((member) => member.user_id === user);
      const failure = refused(route);
      if (failure) return problem(route, failure);
      if (!found) return problem(route, { status: 404, code: 'RESOURCE_NOT_FOUND' });
      const request = route.request();
      const body = request.postDataJSON() as {
        role?: Membership['role'];
        version: number;
        reason: string;
      };
      if (body.version !== found.version) {
        return problem(route, { status: 409, code: 'VERSION_CONFLICT' });
      }
      const owners = members.filter((member) => member.role === 'owner').length;
      const leaving = request.method() === 'DELETE' || body.role !== 'owner';
      if (found.role === 'owner' && owners === 1 && leaving) {
        return problem(route, { status: 409, code: 'LAST_WORKSPACE_OWNER' });
      }
      state.workspaceReasons.push(body.reason);
      if (request.method() === 'DELETE') {
        state.members[id] = members.filter((member) => member.user_id !== user);
        return route.fulfill({ status: 204 });
      }
      found.role = body.role ?? found.role;
      found.version += 1;
      return data(route, found);
    },
  );
}
