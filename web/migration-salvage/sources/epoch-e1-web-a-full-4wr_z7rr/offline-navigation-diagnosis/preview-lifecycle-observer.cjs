'use strict';

// Private, opt-in diagnostics. No test selection, timeout, networking or signal changes.
(() => {
  const argv = process.argv.slice(1);
  const preview = /(?:^|[\\/])vite(?:[\\/]|\.js$)/.test(argv[0] || '') && argv.includes('preview');
  const runner = /playwright/.test(argv[0] || '') && argv.includes('test');
  const directory = process.env.OWNSOLID_PREVIEW_OBSERVER_DIRECTORY;
  if (!directory || (!preview && !runner)) return;

  const fs = require('node:fs');
  const path = require('node:path');
  const { randomUUID } = require('node:crypto');
  const { performance } = require('node:perf_hooks');
  const { errorMonitor } = require('node:events');
  const rawStderr = process.stderr.write;
  const nonce = randomUUID();
  let descriptor;
  let failed = false;
  let sequence = 0;

  function unavailable() {
    if (failed) return;
    failed = true;
    try { Reflect.apply(rawStderr, process.stderr, ['[preview-observer-unavailable]\n']); } catch {}
  }

  try {
    const info = fs.lstatSync(directory);
    if (!info.isDirectory() || info.isSymbolicLink() || info.uid !== process.getuid() || (info.mode & 0o777) !== 0o700) {
      throw new Error('Private observer directory identity mismatch');
    }
    descriptor = fs.openSync(path.join(directory, `${process.pid}-${nonce}.jsonl`), fs.constants.O_WRONLY | fs.constants.O_CREAT | fs.constants.O_EXCL | fs.constants.O_NOFOLLOW, 0o600);
  } catch {
    unavailable();
    return;
  }

  function record(event, details = {}) {
    if (failed) return;
    try {
      fs.writeSync(descriptor, JSON.stringify({ sequence: ++sequence, utc: new Date().toISOString(), monotonicNs: process.hrtime.bigint().toString(), pid: process.pid, ppid: process.ppid, nonce, role: preview ? 'vite-preview' : 'playwright-runner', event, ...details }) + '\n');
    } catch {
      unavailable();
    }
  }

  record('node-bootstrap', { nodeTimeOriginMilliseconds: performance.timeOrigin, kernelBirthClaimed: false });
  process.on('uncaughtExceptionMonitor', (error, origin) => record('uncaught-exception-monitor', { origin, code: error.code, name: error.name }));
  process.on('exit', (code) => {
    record('normal-node-exit', { code });
    try { fs.closeSync(descriptor); } catch { unavailable(); }
  });
  // Do not install signal handlers: Node's default termination must remain intact.

  if (runner) {
    const childProcess = require('node:child_process');
    const originalSpawn = childProcess.spawn;
    childProcess.spawn = function () {
      const command = arguments[0];
      const selected = typeof command === 'string' && /^pnpm exec vite preview --port (31036|31037) --strictPort$/.test(command);
      const child = Reflect.apply(originalSpawn, this, arguments);
      if (!selected) return child;
      // The ChildProcess handle binds spawn/exit to one lifetime, even if a PID is reused later.
      const execution = randomUUID();
      record('preview-spawn-returned', { execution, childPid: child.pid, command });
      child.once('spawn', () => record('preview-spawned', { execution, childPid: child.pid }));
      child.on(errorMonitor, (error) => record('preview-spawn-error', { execution, childPid: child.pid, code: error.code, syscall: error.syscall }));
      child.once('exit', (code, signal) => record('preview-launcher-exit', { execution, childPid: child.pid, code, signal }));
      child.once('close', (code, signal) => record('preview-launcher-streams-closed', { execution, childPid: child.pid, code, signal }));
      return child;
    };
  }

  if (preview) {
    const net = require('node:net');
    const originalListen = net.Server.prototype.listen;
    const watched = new WeakSet();
    net.Server.prototype.listen = function () {
      if (!watched.has(this)) {
        watched.add(this);
        const server = randomUUID();
        this.on('listening', () => record('preview-listening', { server, address: this.address() }));
        this.on('close', () => record('preview-listener-closed', { server }));
        // errorMonitor observes without consuming an otherwise unhandled error.
        this.on(errorMonitor, (error) => record('preview-listener-error', { server, code: error.code, syscall: error.syscall, address: error.address, port: error.port }));
      }
      return Reflect.apply(originalListen, this, arguments);
    };
    for (const channel of ['stdout', 'stderr']) {
      const stream = process[channel];
      const originalWrite = stream.write;
      stream.write = function () {
        const result = Reflect.apply(originalWrite, this, arguments);
        try {
          const chunk = arguments[0];
          const encoding = typeof arguments[1] === 'string' ? arguments[1] : undefined;
          const bytes = typeof chunk === 'string' ? Buffer.from(chunk, encoding) : Buffer.from(chunk);
          record('preview-output', { channel, bytesBase64: bytes.toString('base64') });
        } catch { unavailable(); }
        return result;
      };
    }
  }
})();
