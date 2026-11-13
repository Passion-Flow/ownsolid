"""Resume only a checksum-confirmed prefix, awaiting each durable ACK."""
import hashlib
import json
import os
from pathlib import Path
import select
import shlex
import signal
import subprocess
import time

os.umask(0o077)
root = Path(__file__).resolve().parent
cfg = json.loads((root / 'ack-transfer-config.json').read_text())
packed = (root / 'client-transfer.gz').read_bytes()
assert len(packed) == cfg['size'] and hashlib.sha256(packed).hexdigest() == cfg['gzip_sha256']
receiver = (root / 'ack_receiver.py').read_text()
command = shlex.join(['python3', '-u', '-c', receiver, json.dumps(cfg, separators=(',', ':'))])
ssh = ['ssh', '-T', '-S', 'none', '-o', 'BatchMode=yes', '-o', 'ConnectTimeout=20',
       '-o', 'ServerAliveInterval=30', '-o', 'ServerAliveCountMax=10', 'ownsolid-cpu', command]
log = root / 'ack-transfer-events.jsonl'

def record(value):
    value['local_time'] = time.time()
    with log.open('a') as stream:
        stream.write(json.dumps(value) + '\n')

class Connection:
    def __init__(self):
        self.process = subprocess.Popen(ssh, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                        stderr=subprocess.PIPE, start_new_session=True)
        self.buffer = bytearray()
        self.errors = bytearray()
        for stream in (self.process.stdin, self.process.stdout, self.process.stderr):
            os.set_blocking(stream.fileno(), False)
    def read(self):
        deadline = time.monotonic() + 240
        while b'\n' not in self.buffer:
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise TimeoutError('Timed out awaiting receiver ACK')
            ready, _, _ = select.select([self.process.stdout, self.process.stderr], [], [], remaining)
            for stream in ready:
                data = os.read(stream.fileno(), 65536)
                if stream is self.process.stderr:
                    self.errors.extend(data[:max(0, 4096-len(self.errors))])
                elif data:
                    self.buffer.extend(data)
                else:
                    raise RuntimeError('Receiver ended before expected ACK: ' + self.errors.decode(errors='replace'))
            assert len(self.buffer) <= 65536
        line, _, rest = self.buffer.partition(b'\n')
        self.buffer = bytearray(rest)
        event = json.loads(line)
        record(event)
        return event
    def write(self, data):
        deadline = time.monotonic() + 240
        offset = 0
        while offset < len(data):
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise TimeoutError('Timed out sending bounded frame')
            _, ready, _ = select.select([], [self.process.stdin], [], remaining)
            if ready:
                offset += os.write(self.process.stdin.fileno(), data[offset:])
    def close(self, success):
        if not self.process.stdin.closed:
            self.process.stdin.close()
        if success:
            try:
                code = self.process.wait(timeout=10)
                if code != 0:
                    raise RuntimeError('Receiver exited ' + str(code))
                return
            except subprocess.TimeoutExpired:
                pass
        if self.process.poll() is None:
            os.killpg(self.process.pid, signal.SIGTERM)
            try:
                self.process.wait(timeout=3)
            except subprocess.TimeoutExpired:
                os.killpg(self.process.pid, signal.SIGKILL)
                self.process.wait(timeout=3)
        self.process.stdout.close()
        self.process.stderr.close()

confirmed = cfg['seed_size']
no_progress = 0
for connection_index in range(1, 129):
    connection = Connection()
    succeeded = False
    before = confirmed
    complete = False
    try:
        ready = connection.read()
        assert ready['event'] == 'ready'
        offset = ready['offset']
        assert confirmed <= offset <= len(packed)
        assert hashlib.sha256(packed[:offset]).hexdigest() == ready['prefix_sha256'], 'Divergent remote prefix'
        confirmed = offset
        for _ in range(2):
            if confirmed == len(packed):
                break
            data = packed[confirmed:confirmed+16384]
            checksum = hashlib.sha256(data).hexdigest()
            connection.write((str(len(data)) + ' ' + checksum + '\n').encode() + data)
            ack = connection.read()
            assert ack['event'] == 'chunk' and ack['offset'] == confirmed + len(data)
            assert ack['chunk_sha256'] == checksum
            confirmed += len(data)
        result = connection.read()
        if confirmed == len(packed):
            assert result['event'] == 'complete' and result['binary_sha256'] == cfg['elf_sha256']
            (root / 'ack-receipt.json').write_text(json.dumps(result, indent=2) + '\n')
            complete = True
        else:
            assert result['event'] == 'paused' and result['offset'] == confirmed
            assert result['prefix_sha256'] == hashlib.sha256(packed[:confirmed]).hexdigest()
        succeeded = True
    except Exception as error:
        record({'event': 'sender_error', 'connection': connection_index, 'confirmed': confirmed,
                'error': str(error)})
        print('CONNECTION_STOP ' + str(connection_index) + ' ' + str(error), flush=True)
    finally:
        connection.close(succeeded)
    no_progress = no_progress + 1 if confirmed == before else 0
    if no_progress >= 2:
        raise SystemExit('Stopped after two connections without new confirmed bytes')
    if complete:
        print('CLIENT_RECEIVED_AND_ELF_VERIFIED ' + str(confirmed), flush=True)
        break
    if connection_index % 4 == 0 or not succeeded:
        print('ACK_PROGRESS ' + str(confirmed) + '/' + str(len(packed)), flush=True)
else:
    raise SystemExit('Connection budget exhausted; artifact remains unqualified')
