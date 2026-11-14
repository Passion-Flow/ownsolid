"""Supervise child lifetime in a separate small process, independent of the GIL."""
import atexit
import os
import signal
import time
from .contract_generated import CONTRACT


def arm():
    control = CONTRACT['process_control']
    names = [control['parent_pid_environment'], control['wall_seconds_environment'], control['poll_milliseconds_environment']]
    values = [os.environ.get(name) for name in names]
    if all(value is None for value in values):
        return
    if any(value is None or not value.isascii() or not value.isdecimal() for value in values):
        raise ValueError('KNOWLEDGE_WORKER_REQUEST_INVALID')
    parent, seconds, milliseconds = map(int, values)
    if not 1 <= parent <= 2147483647 or parent != os.getppid() or not 1 <= seconds <= control['maximum_wall_seconds'] or not 100 <= milliseconds <= 2000 or control['termination_signal'] != 'SIGKILL':
        raise ValueError('KNOWLEDGE_WORKER_REQUEST_INVALID')
    deadline = time.monotonic() + seconds
    worker = os.getpid()
    # The installed entrypoint calls this before model imports or worker threads.
    # The supervisor inherits no source payload and immediately drops pipe FDs.
    supervisor = os.fork()
    if supervisor == 0:
        for descriptor in (0, 1, 2):
            try:
                os.close(descriptor)
            except OSError:
                pass
        while os.getppid() == worker:
            alive = True
            try:
                os.kill(parent, 0)
            except ProcessLookupError:
                alive = False
            except PermissionError:
                pass
            if not alive or time.monotonic() >= deadline:
                try:
                    os.kill(worker, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                os._exit(0)
            time.sleep(milliseconds / 1000)
        os._exit(0)

    def stop():
        try:
            os.kill(supervisor, signal.SIGTERM)
        except ProcessLookupError:
            pass
        try:
            os.waitpid(supervisor, 0)
        except ChildProcessError:
            pass

    atexit.register(stop)
