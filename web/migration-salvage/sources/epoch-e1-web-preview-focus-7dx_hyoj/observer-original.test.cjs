'use strict';

// Prepared only: run after a new execution grant. No actual process or socket is created.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const { EventEmitter } = require('node:events');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync(`${__dirname}/observer-original.cjs`, 'utf8');

function load(role = 'preview', writeFailure = false) {
  const writes = [], calls = [], forwarded = [];
  const child = Object.assign(new EventEmitter(), { pid: 456 });
  const process = Object.assign(new EventEmitter(), {
    pid: 123, ppid: 100, getuid: () => 501,
    argv: ['/node', role === 'preview' ? '/pkg/vite/bin/vite.js' : role === 'runner' ? '/pkg/playwright/cli.js' : '/other.js', role === 'runner' ? 'test' : 'preview'],
    env: { OWNSOLID_PREVIEW_OBSERVER_DIRECTORY: '/owned/private' },
    hrtime: { bigint: () => 1n },
  });
  for (const channel of ['stdout', 'stderr']) {
    process[channel] = { write: function () { forwarded.push({ channel, receiver: this, args: [...arguments] }); return false; } };
  }
  class Server extends EventEmitter {
    listen() { calls.push({ kind: 'listen', receiver: this, args: [...arguments] }); return this; }
    address() { return { address: '::1', family: 'IPv6', port: 31036 }; }
  }
  const cp = { spawn: function () { calls.push({ kind: 'spawn', receiver: this, args: [...arguments] }); return child; } };
  let uuid = 0;
  const modules = {
    'node:fs': {
      constants: { O_WRONLY: 1, O_CREAT: 2, O_EXCL: 4, O_NOFOLLOW: 8 },
      lstatSync: () => ({ isDirectory: () => true, isSymbolicLink: () => false, uid: 501, mode: 0o700 }),
      openSync: () => 3,
      writeSync: (_, text) => { if (writeFailure) throw new Error('fixture full'); writes.push(JSON.parse(text)); },
      closeSync: () => {},
    },
    'node:path': require('node:path'),
    'node:crypto': { randomUUID: () => `nonce-${++uuid}` },
    'node:perf_hooks': { performance: { timeOrigin: 10 } },
    'node:events': require('node:events'),
    'node:child_process': cp,
    'node:net': { Server },
  };
  const originalSpawn = cp.spawn, originalListen = Server.prototype.listen;
  vm.runInNewContext(source, { process, Buffer, require: (name) => modules[name] });
  return { process, writes, calls, forwarded, child, cp, Server, originalSpawn, originalListen };
}

test('unrelated Node commands remain inactive', () => {
  const r = load('other');
  assert.equal(r.writes.length, 0);
  assert.equal(r.cp.spawn, r.originalSpawn);
  assert.equal(r.Server.prototype.listen, r.originalListen);
});

test('listen retains receiver, arguments, return and actual address event', () => {
  const r = load(), server = new r.Server(), callback = () => {};
  assert.equal(server.listen(31036, 'localhost', callback), server);
  assert.equal(r.calls[0].receiver, server);
  assert.deepEqual(r.calls[0].args, [31036, 'localhost', callback]);
  server.emit('listening');
  assert.deepEqual(r.writes.find((x) => x.event === 'preview-listening').address, server.address());
  const error = Object.assign(new Error('fixture'), { code: 'EADDRINUSE' });
  assert.throws(() => server.emit('error', error), (actual) => actual === error);
  assert.equal(r.writes.find((x) => x.event === 'preview-listener-error').code, 'EADDRINUSE');
});

test('spawn retains the exact ChildProcess handle and exposes exit/signal', () => {
  const r = load('runner'), args = [], options = { shell: true };
  assert.equal(r.cp.spawn('pnpm exec vite preview --port 31036 --strictPort', args, options), r.child);
  assert.equal(r.calls[0].args[1], args);
  assert.equal(r.calls[0].args[2], options);
  r.child.emit('spawn'); r.child.emit('exit', null, 'SIGKILL'); r.child.emit('close', null, 'SIGKILL');
  const event = r.writes.find((x) => x.event === 'preview-launcher-exit');
  assert.equal(event.childPid, 456); assert.equal(event.code, null); assert.equal(event.signal, 'SIGKILL');
  assert.equal(r.writes.filter((x) => x.event === 'preview-spawned').length, 1);
});

test('output preserves bytes, write return, receiver and callback identity', () => {
  const r = load(), bytes = Buffer.from([0, 255, 10]), callback = () => {};
  assert.equal(r.process.stderr.write(bytes, callback), false);
  assert.equal(r.forwarded[0].receiver, r.process.stderr);
  assert.equal(r.forwarded[0].args[0], bytes); assert.equal(r.forwarded[0].args[1], callback);
  assert.equal(r.writes.find((x) => x.event === 'preview-output').bytesBase64, bytes.toString('base64'));
});

test('logging failure is visible once and retains original writes', () => {
  const r = load('preview', true);
  assert.equal(r.process.stdout.write('fixture'), false);
  assert.equal(r.forwarded.filter((x) => x.args[0] === '[preview-observer-unavailable]\n').length, 1);
  assert.equal(r.forwarded.at(-1).args[0], 'fixture');
});

test('signal defaults remain untouched and normal exit preserves its code', () => {
  const r = load();
  for (const signal of ['SIGINT', 'SIGTERM', 'SIGHUP']) assert.equal(r.process.listenerCount(signal), 0);
  r.process.emit('exit', 23);
  assert.equal(r.writes.find((x) => x.event === 'normal-node-exit').code, 23);
});
