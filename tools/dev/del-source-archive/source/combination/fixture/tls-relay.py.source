"""Acceptance-only opaque relay from a private pathname socket to loopback TCP."""
import asyncio
import os
import signal
import stat
import sys
from pathlib import Path


async def main(path, port):
    parent = path.parent.stat()
    if not path.is_absolute() or path.parent.resolve() != path.parent:
        raise ValueError("Use a canonical private socket directory")
    if parent.st_uid != os.geteuid() or stat.S_IMODE(parent.st_mode) != 0o700:
        raise ValueError("Use an owned directory with mode 0700")
    if path.exists() or path.is_symlink() or not 1024 <= port <= 65535:
        raise ValueError("Use an unused socket and an unprivileged target port")
    stopped = asyncio.Event()
    tasks = set()

    async def forward(reader, writer):
        while True:
            data = await asyncio.wait_for(reader.read(65536), 900)
            if not data:
                if writer.can_write_eof():
                    writer.write_eof()
                    await writer.drain()
                return
            writer.write(data)
            await asyncio.wait_for(writer.drain(), 30)

    async def accepted(reader, writer):
        task = asyncio.current_task()
        if len(tasks) >= 32:
            writer.close()
            await writer.wait_closed()
            return
        tasks.add(task)
        remote = None
        try:
            upstream, remote = await asyncio.wait_for(
                asyncio.open_connection("127.0.0.1", port), 5
            )
            pipes = [asyncio.create_task(forward(reader, remote)),
                     asyncio.create_task(forward(upstream, writer))]
            try:
                await asyncio.gather(*pipes)
            finally:
                for pipe in pipes:
                    pipe.cancel()
                await asyncio.gather(*pipes, return_exceptions=True)
        except (ConnectionError, TimeoutError, OSError):
            pass
        finally:
            for stream in (remote, writer):
                if stream is not None:
                    stream.close()
                    try:
                        await asyncio.wait_for(stream.wait_closed(), 5)
                    except (ConnectionError, TimeoutError, OSError):
                        pass
            tasks.discard(task)

    os.umask(0o077)
    server = await asyncio.start_unix_server(accepted, path=path, backlog=32)
    os.chmod(path, 0o600)
    identity = path.stat()
    loop = asyncio.get_running_loop()
    for sig in (signal.SIGTERM, signal.SIGINT):
        loop.add_signal_handler(sig, stopped.set)
    try:
        async with server:
            print("PRIVATE_TLS_RELAY_READY", flush=True)
            await stopped.wait()
    finally:
        for task in tasks.copy():
            task.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)
        current = path.lstat()
        if (current.st_dev, current.st_ino) == (identity.st_dev, identity.st_ino):
            path.unlink()


if __name__ == "__main__":
    asyncio.run(main(Path(sys.argv[1]), int(sys.argv[2])))
