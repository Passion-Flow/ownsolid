import assert from 'node:assert/strict';
import { randomUUID } from 'node:crypto';
import { createRequire } from 'node:module';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { readFileSync, writeFileSync } from 'node:fs';
const root = dirname(fileURLToPath(import.meta.url));
const input = JSON.parse(readFileSync(join(root, 'input.json'), 'utf8'));
const require = createRequire(join(input.worktree, 'web/apps/console/package.json'));
const { QueryClient, QueryCache, InfiniteQueryObserver, focusManager, onlineManager } = require('@tanstack/react-query');
const actualVersion = require('@tanstack/react-query/package.json').version;
assert.equal(actualVersion, input.queryVersion);
const checks = [];
const pending = [];
const calls = [];
const identity = { user: { id: 'synthetic-person' }, audience: 'portal' };
let client;
client = new QueryClient({ queryCache: new QueryCache({ onError: (error) => {
  if (error.status === 401) client.setQueryData(['session'], null);
} }) });
client.mount();
client.setQueryData(['session'], identity);
function deferred() {
  let resolve, reject;
  const promise = new Promise((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
function reply(page) { pending.push({ promise: Promise.resolve(page) }); }
function page(current, cursor = null) { return { items: [{ id: current, current: true }], next_cursor: cursor }; }
function until(view, predicate) {
  if (predicate(view.observer.getCurrentResult())) return Promise.resolve();
  return new Promise((resolve) => {
    const off = view.observer.subscribe((value) => { if (predicate(value)) { off(); resolve(); } });
  });
}
function mount(stable = false) {
  const viewId = stable ? 'naive-shared-view' : randomUUID();
  const key = ['own-browser-sessions', identity.audience, identity.user.id, viewId];
  const options = {
    queryKey: key,
    queryFn: ({ pageParam, signal }) => {
      const next = pending.shift();
      assert.ok(next, 'No implicit/unplanned fixture read');
      calls.push({ viewId, cursor: pageParam, signal });
      return next.promise;
    },
    initialPageParam: null,
    getNextPageParam: (last) => last.next_cursor,
    enabled: (query) => query.state.status !== 'error',
    retry: false, retryOnMount: false, refetchOnMount: false,
    refetchOnWindowFocus: false, refetchOnReconnect: false,
    networkMode: 'always', staleTime: 'static',
    // Deliberately retain every old snapshot: isolation cannot pass by relying on GC.
    gcTime: Infinity,
  };
  const observer = new InfiniteQueryObserver(client, options);
  const off = observer.subscribe(() => undefined);
  return { key, observer, options, off, result: () => observer.getCurrentResult(), visible: () => {
    const result = observer.getCurrentResult();
    return [401, 403, 404].includes(result.error?.status) ? undefined : result.data;
  }, readAgain: () => client.infiniteQuery({ ...options, pages: 1, staleTime: 0 }) };
}
const views = [];
try {
  reply(page('old-current'));
  const baseline = mount(true); views.push(baseline);
  await until(baseline, (r) => r.isSuccess);
  baseline.off();
  client.setQueryData(['session'], null);
  client.setQueryData(['session'], identity);
  const naiveAgain = mount(true); views.push(naiveAgain);
  assert.equal(naiveAgain.visible().pages[0].items[0].id, 'old-current');
  assert.equal(calls.length, 1);
  naiveAgain.off();
  checks.push('Negative control: a naive reused key exposes the retained old Current marker without a read');

  const gate = deferred(); pending.push(gate);
  const fresh = mount(); views.push(fresh);
  assert.equal(fresh.result().data, undefined);
  assert.ok(client.getQueryData(baseline.key));
  checks.push('Fresh per-view cache identity withholds old data even when old snapshots remain cached');
  gate.reject(Object.assign(new Error('Synthetic denial'), { status: 403 }));
  await until(fresh, (r) => r.isError);
  assert.equal(fresh.visible(), undefined);
  checks.push('A refused first read never reveals the retained earlier session snapshot');

  reply(page('fresh-current', 'next'));
  await fresh.readAgain();
  await until(fresh, (r) => r.isSuccess);
  assert.equal(fresh.visible().pages[0].items[0].id, 'fresh-current');
  assert.equal(calls.length, 3);
  checks.push('One explicit recovery reads a current marker from the current view');

  const late = deferred(); pending.push(late);
  const paging = fresh.result().fetchNextPage({ cancelRefetch: false }).catch(() => undefined);
  const signal = calls.at(-1).signal;
  fresh.off();
  assert.equal(signal.aborted, true);
  reply(page('next-view-current'));
  const nextView = mount(); views.push(nextView);
  await until(nextView, (r) => r.isSuccess);
  late.resolve(page('late-old-peer'));
  await paging;
  assert.deepEqual(nextView.visible().pages.flatMap((p) => p.items.map((r) => r.id)), ['next-view-current']);
  checks.push('Leaving cancels the pending page; its late answer cannot enter a new view');

  nextView.off();
  const rotated = deferred(); pending.push(rotated);
  const afterPassword = mount(); views.push(afterPassword);
  assert.equal(afterPassword.result().data, undefined);
  rotated.resolve(page('after-password-current'));
  await until(afterPassword, (r) => r.isSuccess);
  assert.equal(afterPassword.visible().pages[0].items[0].id, 'after-password-current');
  checks.push('Explicit view reset after credential rotation does not reuse a previous Current marker');

  pending.push({ promise: Promise.reject(Object.assign(new Error('Synthetic expiry'), { status: 401 })) });
  await afterPassword.readAgain().catch(() => undefined);
  await until(afterPassword, (r) => r.isError);
  assert.equal(client.getQueryData(['session']), null);
  assert.ok(afterPassword.result().data, 'The global handler intentionally did not clear cached pages');
  assert.equal(afterPassword.visible(), undefined);
  checks.push('The real handler-style null-session transition hides data even with cached pages retained');

  const before = calls.length;
  focusManager.setFocused(false); focusManager.setFocused(true);
  onlineManager.setOnline(false); onlineManager.setOnline(true);
  await client.invalidateQueries({ queryKey: ['own-browser-sessions'] });
  assert.equal(calls.length, before);
  assert.equal(pending.length, 0);
  checks.push('Expiry is not replayed by focus, reconnect or invalidation');
  const result = { version: actualVersion, assertionsPassed: checks.length, checks,
    fixtureReadCalls: calls.length, deniedReads: 2, cancelledPages: 1,
    scope: 'TanStack observer/cache prototype only; synthetic rows, no React mount/StrictMode, HTTP, browser, SQL or durable revocation',
    network: false, browser: false, productSourceChanged: false,
    strategy: 'ephemeral view key plus explicit reset on auth/credential transition; server authority remains unchanged' };
  writeFileSync(join(root, 'result.json'), JSON.stringify(result, null, 2) + '\n');
  console.log(JSON.stringify(result, null, 2));
} finally {
  for (const view of views) view.off();
  client.clear(); client.unmount();
  focusManager.setFocused(undefined); onlineManager.setOnline(true);
}
