import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
const require = createRequire("/Users/passion./Documents/Passion's WorkSpace/Development/lane-web/web/apps/console/package.json");
const { QueryClient, InfiniteQueryObserver } = require('@tanstack/react-query');
const client = new QueryClient({ defaultOptions: { queries: { gcTime: Infinity } } });
const key = ['prototype-only', 'resource-activity', 'recent'];
const requests = [];
let now = Date.parse('2026-11-13T00:00:00Z');
let fail = false;
const day = 86_400_000;
async function serverRead(input) {
  requests.push({ ...input });
  if (fail) { fail = false; throw new Error('Prototype response loss'); }
  if (input.cursor) {
    // This is the server fixture, not the client: cursor contents remain opaque to the client.
    const pinned = JSON.parse(input.cursor);
    assert.equal(input.from, undefined);
    assert.equal(input.to, undefined);
    return { items: [], next_cursor: null, serverWindow: pinned };
  }
  assert.equal(Date.parse(input.to) - Date.parse(input.from), day);
  return { items: [], next_cursor: JSON.stringify({ from: input.from, to: input.to }) };
}
const options = {
  queryKey: key,
  queryFn: async ({ pageParam }) => {
    const bounds = pageParam === null ? { from: new Date(now - day).toISOString(), to: new Date(now).toISOString() } : undefined;
    const page = await serverRead({ cursor: pageParam, ...bounds });
    return { items: page.items, next_cursor: page.next_cursor, clientWindow: bounds };
  },
  initialPageParam: null,
  getNextPageParam: page => page.next_cursor,
  enabled: query => query.state.status !== 'error',
  retry: false, retryOnMount: false, refetchOnMount: false,
  refetchOnWindowFocus: false, refetchOnReconnect: false, refetchInterval: false,
  staleTime: 'static', networkMode: 'always',
};
let observer = new InfiniteQueryObserver(client, options);
let unsubscribe = observer.subscribe(() => {});
await client.infiniteQuery(options);
assert.equal(requests.length, 1);
const first = client.getQueryData(key).pages[0].clientWindow;
await observer.fetchNextPage({ cancelRefetch: false });
assert.equal(requests.length, 2);
assert.equal(client.getQueryData(key).pages.length, 2);
assert.deepEqual(client.getQueryData(key).pages[0].clientWindow, first);
unsubscribe(); observer.destroy();
now += 2 * day;
observer = new InfiniteQueryObserver(client, options);
unsubscribe = observer.subscribe(() => {});
await new Promise(resolve => setTimeout(resolve, 0));
assert.equal(requests.length, 2, 'Remount must not silently read a new moving window');
fail = true;
await assert.rejects(client.infiniteQuery({ ...options, pages: 1, staleTime: 0 }));
assert.equal(requests.length, 3);
assert.equal(client.getQueryData(key).pages.length, 2, 'Unknown response must retain the old pages');
assert.deepEqual(client.getQueryData(key).pages[0].clientWindow, first);
await client.invalidateQueries({ queryKey: key });
await new Promise(resolve => setTimeout(resolve, 0));
assert.equal(requests.length, 3, 'Invalidation must not replay an uncertain read');
await client.infiniteQuery({ ...options, pages: 1, staleTime: 0 });
assert.equal(requests.length, 4, 'One explicit recovery is one request');
const current = client.getQueryData(key);
assert.equal(current.pages.length, 1);
assert.equal(current.pages[0].clientWindow.to, new Date(now).toISOString());
assert.notDeepEqual(current.pages[0].clientWindow, first);
unsubscribe(); observer.destroy(); client.clear();
console.log(JSON.stringify({ scope: 'Isolated query-cache prototype, not product/server/browser acceptance', checks: ['24h initial bounds', 'server-pinned next cursor with omitted client bounds', 'initial metadata retained through pagination', 'remount makes no request', 'unknown response preserves two old pages and old bounds', 'invalidation makes no request', 'explicit recovery makes exactly one request', 'successful recovery replaces one page and its bounds'], checksPassed: 8, requests: requests.length }, null, 2));
