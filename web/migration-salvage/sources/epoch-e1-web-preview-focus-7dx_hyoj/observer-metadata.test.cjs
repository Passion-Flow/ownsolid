const { test } = require('node:test');
const assert = require('node:assert/strict');
const { EventEmitter } = require('node:events');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync(`${__dirname}/preview-lifecycle-observer.cjs`, 'utf8');
function load(role) {
  const writes = [], child = Object.assign(new EventEmitter(), { pid: 456, stderr: new EventEmitter() });
  const write = () => false;
  const process = Object.assign(new EventEmitter(), { pid: 123, ppid: 100, getuid: () => 501,
    argv: ['/node', role === 'preview' ? '/pkg/vite/bin/vite.js' : '/pkg/playwright/cli.js', role === 'preview' ? 'preview' : 'test'],
    env: { OWNSOLID_PREVIEW_OBSERVER_DIRECTORY: '/owned/private', OWNSOLID_PREVIEW_OBSERVER_METADATA_ONLY: '1' },
    hrtime: { bigint: () => 1n }, stdout: { write }, stderr: { write } });
  class Server extends EventEmitter { listen() { return this; } address() { return { address: '::1', family: 'IPv6', port: 31036 }; } }
  const cp = { spawn: () => child };
  const modules = { 'node:fs': { constants: { O_WRONLY: 1, O_CREAT: 2, O_EXCL: 4, O_NOFOLLOW: 8 }, lstatSync: () => ({ isDirectory: () => true, isSymbolicLink: () => false, uid: 501, mode: 0o700 }), openSync: () => 3, writeSync: (_, s) => writes.push(JSON.parse(s)), closeSync: () => {} },
    'node:path': require('node:path'), 'node:crypto': require('node:crypto'), 'node:perf_hooks': require('node:perf_hooks'), 'node:events': require('node:events'), 'node:net': { Server }, 'node:child_process': cp };
  vm.runInNewContext(source, { process, Buffer, require: (name) => modules[name] });
  return { writes, child, process, write, Server, cp };
}
test('metadata mode leaves output writes untouched and still observes listen identity', () => {
  const r = load('preview');
  assert.equal(r.process.stdout.write, r.write); assert.equal(r.process.stderr.write, r.write);
  const server = new r.Server(); server.listen(31036); server.emit('listening');
  assert.equal(r.writes.find((x) => x.event === 'preview-listening').address.port, 31036);
  assert.equal(r.writes.some((x) => x.event === 'preview-output'), false);
});
test('launcher stderr records digest and error codes without raw content', () => {
  const r = load('runner'); r.cp.spawn('pnpm exec vite preview --port 31036 --strictPort', [], {});
  const bytes = Buffer.from('sample-sensitive-text EADDRINUSE'); r.child.stderr.emit('data', bytes);
  const event = r.writes.find((x) => x.event === 'preview-launcher-stderr');
  assert.equal(event.bytes, bytes.length); assert.equal(event.sha256, require('node:crypto').createHash('sha256').update(bytes).digest('hex'));
  assert.deepEqual(event.errorCodes, ['EADDRINUSE']);
  assert.equal(JSON.stringify(r.writes).includes('sample-sensitive-text'), false);
  assert.equal('bytesBase64' in event, false);
});
