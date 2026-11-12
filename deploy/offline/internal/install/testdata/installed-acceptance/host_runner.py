"""Run one admitted Linux installation; metadata never executes arbitrary commands."""
import argparse
from contextlib import ExitStack
import fcntl
import json
import os
from pathlib import Path
import re
import signal
import stat
import subprocess
import sys
import time
import uuid

import acceptance_guard as guard

FIELDS = {'output_directory', 'run_id', 'container_name', 'docker_socket', 'heavy_locks',
          'hard_timeout_seconds', 'cleanup_seconds', 'startup_seconds', 'suite_seconds',
          'operation_seconds', 'cpus', 'memory_bytes', 'docker_tmpfs_bytes', 'pids_limit', 'images',
          'minimum_free_memory_bytes', 'minimum_available_memory_bytes', 'minimum_disk_bytes',
          'maximum_io_avg10', 'relay_port'}


def configuration(root, phases=None):
    """Require explicit resources, image identities and an unused evidence path."""
    value = guard.read_json(root / 'runtime.json')
    guard.require(set(value) == FIELDS, 'Unknown or missing runtime setting')
    guard.require(isinstance(value['run_id'], str) and str(uuid.UUID(value['run_id'])) == value['run_id'], 'Invalid run UUID')
    guard.require(re.fullmatch('lane-del-[a-z0-9-]{1,48}', value['container_name']), 'Use a DEL-only container name')
    for key in FIELDS - {'output_directory', 'run_id', 'container_name', 'docker_socket',
                         'heavy_locks', 'images', 'maximum_io_avg10'}:
        guard.require(type(value[key]) is int and value[key] > 0, 'Use a positive integer for ' + key)
    guard.require(value['suite_seconds'] + value['startup_seconds'] + value['cleanup_seconds']
                  < value['hard_timeout_seconds'], 'Reserve cleanup time before the hard deadline')
    guard.require(value['docker_tmpfs_bytes'] < value['memory_bytes'], 'Keep Docker scratch below the memory cap')
    guard.require(1 <= value['relay_port'] <= 65535 and value['cpus'] <= (os.cpu_count() or 1), 'Invalid port or CPU cap')
    guard.require(type(value['maximum_io_avg10']) in (int, float)
                  and 0 <= value['maximum_io_avg10'] <= 100, 'Invalid I/O threshold')
    expected_images = {'dind', 'control', 'postgres', 'metrics', 'logs'}
    if 'obs' in (['obs'] if phases is None else phases):
        expected_images.add('evaluator')
    guard.require(isinstance(value['images'], dict) and set(value['images']) == expected_images
                  and all(re.fullmatch('sha256:[0-9a-f]{64}', item) for item in value['images'].values()),
                  'Pin every image by immutable local identity')
    guard.require(isinstance(value['heavy_locks'], list) and len(value['heavy_locks']) == 2
                  and all(isinstance(path, str) and Path(path).is_absolute() for path in value['heavy_locks'])
                  and len(set(value['heavy_locks'])) == 2, 'Configure both ordered host locks')
    output = Path(value['output_directory'])
    guard.require(output.is_absolute() and output.parent.resolve() == output.parent
                  and not os.path.lexists(output), 'Use a fresh canonical evidence directory')
    socket = Path(value['docker_socket'])
    guard.require(socket.is_absolute() and stat.S_ISSOCK(socket.stat().st_mode), 'Select a local Unix Docker socket')
    return value


def resources(config):
    """Check the shared-host gate under the actual parent locks."""
    memory = {line.split(':')[0]: int(line.split()[1]) * 1024
              for line in Path('/proc/meminfo').read_text().splitlines()}
    guard.require(memory['MemFree'] >= config['minimum_free_memory_bytes']
                  and memory['MemAvailable'] >= config['minimum_available_memory_bytes'], 'Host memory gate failed')
    disk = os.statvfs(config['output_directory'])
    guard.require(disk.f_bavail * disk.f_frsize >= config['minimum_disk_bytes'], 'Host disk gate failed')
    pressure = Path('/proc/pressure/io').read_text()
    guard.require(all(float(line.split('avg10=')[1].split()[0]) <= config['maximum_io_avg10']
                      for line in pressure.splitlines()), 'Host I/O pressure gate failed')
    return {'memfree_bytes': memory['MemFree'], 'memavailable_bytes': memory['MemAvailable'], 'io': pressure}


class Window:
    """Own one container and relay, preserving capture on every exit path."""
    def __init__(self, root, config, admission):
        self.root, self.config, self.admission = root, config, admission
        self.output = Path(config['output_directory'])
        self.name = config['container_name']
        self.relay = None
        self.created = False
        self.result = None
        self.hard_deadline = None
        self.finished = False
        self.cleanup_errors = []
        self.preparer = None
        self.monotonic_deadline = None

    def docker(self, *args, timeout=None, check=True):
        return subprocess.run(['docker', '--host', 'unix://' + self.config['docker_socket'], *args],
                              check=check, capture_output=True, text=True,
                              timeout=timeout or self.config['operation_seconds'])

    def inspect(self):
        result = self.docker('container', 'inspect', self.name, check=False)
        if result.returncode:
            return None
        value = json.loads(result.stdout)[0]
        guard.require(value['Config']['Labels'].get('io.ownsolid.acceptance.run_id') == self.config['run_id'],
                      'Refuse to operate on a foreign container')
        return value

    def stop_relay(self):
        if self.relay is not None:
            self.relay.terminate()
            try:
                self.relay.wait(timeout=self.config['operation_seconds'])
            except subprocess.TimeoutExpired:
                self.relay.kill()
                self.relay.wait()
            self.relay = None

    def stop_preparer(self):
        if self.preparer is not None:
            if self.preparer.poll() is None:
                self.preparer.terminate()
                try:
                    self.preparer.wait(timeout=self.config['operation_seconds'])
                except subprocess.TimeoutExpired:
                    self.preparer.kill()
                    self.preparer.wait()
            self.preparer = None

    def launch(self):
        guard.require(self.docker('container', 'inspect', self.name, check=False).returncode != 0,
                      'Container name is occupied; nothing was started')
        images = self.config['images']
        image = json.loads(self.docker('image', 'inspect', images['dind']).stdout)[0]
        guard.require(image['Os'] == 'linux' and image['Architecture'] == 'amd64', 'Load the Linux amd64 DIND image')
        self.hard_deadline = int(time.time()) + self.config['hard_timeout_seconds']
        self.monotonic_deadline = time.monotonic() + self.config['hard_timeout_seconds']
        guard.initialize_control(self.output, self.config['run_id'], self.admission['candidate'],
                                 self.hard_deadline, self.admission['phases'])
        args = ['run', '--detach', '--pull', 'never', '--name', self.name, '--hostname', 'lane-del-installed',
                '--add-host', 'lane-del-installed:127.0.0.1', '--network', 'none', '--privileged',
                '--cgroupns', 'private', '--cpus', str(self.config['cpus']), '--memory', str(self.config['memory_bytes']),
                '--pids-limit', str(self.config['pids_limit']),
                '--tmpfs', '/var/lib/docker:size=' + str(self.config['docker_tmpfs_bytes']),
                '--label', 'io.ownsolid.acceptance.run_id=' + self.config['run_id'],
                '--volume', str(self.root) + ':/bundle:ro', '--volume', str(self.output) + ':/evidence',
                '--env', 'DOCKER_TLS_CERTDIR=', '--env', 'COMBINED_SOURCE_SHA=' + self.admission['candidate'],
                '--env', 'ACCEPTANCE_RUN_ID=' + self.config['run_id'],
                '--env', 'ACCEPTANCE_WITH_HISTORY=' + str('obs' in self.admission['phases']).lower(),
                '--env', 'ACCEPTANCE_STARTUP_SECONDS=' + str(self.config['startup_seconds']),
                '--env', 'ACCEPTANCE_STOP_SECONDS=' + str(self.config['operation_seconds']),
                '--env', 'ACCEPTANCE_SUITE_SECONDS=' + str(self.config['suite_seconds'])]
        for name in ['control', 'evaluator', 'postgres', 'metrics', 'logs']:
            if name in images:
                args += ['--env', name.upper() + '_IMAGE=' + images[name]]
        # Reserve cleanup ownership before a response can be lost after creation.
        self.created = True
        self.docker(*args, '--entrypoint', '/usr/local/bin/dind', images['dind'], '/bin/sh', '/bundle/inside.sh')
        state = self.inspect()
        with (self.output / 'relay.log').open('w') as relay_log:
            self.relay = subprocess.Popen(['nsenter', '--target', str(state['State']['Pid']), '--net',
                sys.executable, str(self.root / 'tls-relay.py'), str(self.output / 'private-tls.sock'),
                str(self.config['relay_port'])], stdout=relay_log, stderr=subprocess.STDOUT)

    def run(self):
        self.launch()
        while True:
            state = self.inspect()
            guard.require(state is not None, 'The owned namespace disappeared before closure')
            if not state['State']['Running']:
                self.result = state['State']['ExitCode']
                return
            guard.require(time.monotonic() < self.monotonic_deadline - self.config['cleanup_seconds'],
                          'Installation work deadline reached; cleanup reserve is active')
            guard.require(self.finished or self.relay.poll() is None, 'The private TLS relay stopped')
            metadata = guard.handoff(self.output, 'DEL', 'status', None)
            if metadata['phase'] == 'prepared' and (self.output / 'acceptance-ready.json').exists():
                guard.handoff(self.output, 'DEL', 'installed-ready', metadata['generation'])
            if metadata['phase'] == 'obs-ready' and not metadata['history_prepared']:
                metadata = guard.handoff(self.output, 'DEL', 'begin-history', metadata['generation'])
            if metadata['phase'] == 'history-preparation' and self.preparer is None:
                with (self.output / 'history-preparation.log').open('w') as log:
                    self.preparer = subprocess.Popen(['nsenter', '--target', str(state['State']['Pid']), '--net',
                        sys.executable, str(self.root / 'prepare-history.py'), '--evidence', str(self.output),
                        '--bundle', str(self.root), '--container', self.name, '--run-id', self.config['run_id'],
                        '--docker-socket', self.config['docker_socket']],
                        stdout=log, stderr=subprocess.STDOUT)
            if metadata['phase'] == 'history-preparation' and self.preparer is not None and self.preparer.poll() is not None:
                code = self.preparer.returncode
                self.preparer = None
                guard.require(code == 0, 'Native history preparation failed; inspect its protected log')
                guard.handoff(self.output, 'DEL', 'history-ready', metadata['generation'])
            if metadata['phase'] == 'failed':
                raise guard.Refused('An owner reported a failed phase')
            if metadata['phase'] == 'complete' and not self.finished:
                self.stop_relay()
                (self.output / 'owners-finished').touch(mode=0o600, exist_ok=False)
                self.finished = True
            time.sleep(1)

    def close(self):
        self.stop_preparer()
        self.stop_relay()
        if not self.created:
            return
        try:
            state = self.inspect()
            if state is not None:
                if state['State']['Running']:
                    remaining = max(1, min(self.config['cleanup_seconds'], int(self.monotonic_deadline - time.monotonic())))
                    try:
                        self.docker('stop', '--timeout', str(remaining), self.name, timeout=remaining + 1, check=False)
                    except (subprocess.SubprocessError, OSError) as error:
                        self.cleanup_errors.append(type(error).__name__)
                try:
                    result = self.docker('logs', self.name, check=False)
                    (self.output / 'container.log').write_text(result.stdout + result.stderr)
                finally:
                    # A failed graceful stop still gets one bounded owned removal.
                    self.docker('rm', '--force', '--volumes', self.name)
                guard.require(self.inspect() is None, 'Owned namespace cleanup is incomplete')
        except (subprocess.SubprocessError, OSError, ValueError) as error:
            self.cleanup_errors.append(type(error).__name__)
        capture = guard.capture_result(self.output, self.config['run_id'])
        guard.write_json(self.output / 'window-result.json', {
            'suite_exit': self.result, 'capture': capture, 'candidate': self.admission['candidate'],
            'cleanup_errors': self.cleanup_errors,
            'passed': not self.cleanup_errors and self.result == 0 and capture['status'] == 'capture-passed'})


def run(root):
    """Admission precedes Docker, locks and the bounded instance."""
    admission = guard.check_inputs(root)
    config = configuration(root, admission['phases'])
    guard.require(sys.platform == 'linux' and os.uname().machine == 'x86_64' and os.geteuid() == 0,
                  'Run the admitted fixture as root on Linux amd64')
    output = Path(config['output_directory'])
    output.mkdir(mode=0o700)
    with ExitStack() as stack:
        leases = []
        for path in config['heavy_locks']:
            descriptor = os.open(path, os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
            stack.callback(os.close, descriptor)
            guard.require(stat.S_ISREG(os.fstat(descriptor).st_mode), 'The lease carrier is not a file')
            fcntl.flock(descriptor, fcntl.LOCK_EX | fcntl.LOCK_NB)
            leases.append({'fd': descriptor, 'path': path, 'inode': os.fstat(descriptor).st_ino})
        facts = resources(config)
        start_ticks = Path('/proc/self/stat').read_text().rsplit(')', 1)[1].split()[19]
        guard.write_json(output / 'parent.json', {'pid': os.getpid(), 'start_ticks': start_ticks, 'leases': leases,
                                                  'admission': admission, 'resources': facts})
        window = Window(root, config, admission)
        def interrupted(signum, frame):
            raise guard.Refused('Parent interrupted by signal ' + str(signum))
        previous = {item: signal.signal(item, interrupted) for item in (signal.SIGTERM, signal.SIGINT, signal.SIGHUP)}
        try:
            window.run()
        finally:
            for item in previous:
                signal.signal(item, signal.SIG_IGN)
            try:
                window.close()
            finally:
                for item, handler in previous.items():
                    signal.signal(item, handler)
        result = guard.read_json(output / 'window-result.json')
        guard.require(result['passed'], 'The installed window did not pass; retain its evidence')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, required=True)
    arguments = parser.parse_args()
    run(arguments.root)
