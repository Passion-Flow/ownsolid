import importlib.util
import os
from pathlib import Path
import select
import signal
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

SOURCE = Path(__file__).with_name("workstation-lock.py")
SPEC = importlib.util.spec_from_file_location("ops_lock", SOURCE)
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)
IMPORT = (
    "import importlib.util, pathlib, sys; "
    "s=importlib.util.spec_from_file_location('lock',sys.argv[1]); "
    "m=importlib.util.module_from_spec(s); s.loader.exec_module(m); "
    "m.LOCK_PATH=pathlib.Path(sys.argv[2]); "
)


class ProtocolTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.path = Path(self.directory.name) / "carrier"

    def test_preserves_existing_carrier(self):
        self.path.write_bytes(b"retain")
        before = self.path.stat()
        fd = MODULE.acquire(self.path)
        os.close(fd)
        self.assertEqual(self.path.stat().st_ino, before.st_ino)
        self.assertEqual(self.path.read_bytes(), b"retain")

    def test_refuses_invalid_carriers(self):
        self.path.mkdir()
        with self.assertRaises(ValueError):
            MODULE.acquire(self.path)
        self.path.rmdir()
        target = self.path.with_name("target")
        target.touch()
        self.path.symlink_to(target)
        with self.assertRaises(ValueError):
            MODULE.acquire(self.path)
        self.path.unlink()
        self.path.touch()
        with patch.object(MODULE.os, "getuid", return_value=os.getuid() + 1):
            with self.assertRaises(ValueError):
                MODULE.acquire(self.path)

    def test_busy_process_is_refused(self):
        fd = MODULE.acquire(self.path)
        try:
            child = subprocess.run(
                [sys.executable, "-c", IMPORT + "m.acquire(m.LOCK_PATH)",
                 str(SOURCE), str(self.path)],
                capture_output=True, text=True, timeout=5,
            )
            self.assertNotEqual(child.returncode, 0)
            self.assertIn("BlockingIOError", child.stderr)
        finally:
            os.close(fd)
        reopened = MODULE.acquire(self.path)
        os.close(reopened)

    def test_child_inherits_locked_descriptor(self):
        child_code = (
            "m.check(); "
            "fd=int(m.os.environ[m.FD_VARIABLE]); "
            "assert m.os.fstat(fd).st_ino == m.LOCK_PATH.stat().st_ino"
        )
        with patch.object(MODULE, "LOCK_PATH", self.path):
            result = MODULE.run(
                [sys.executable, "-c", IMPORT + child_code,
                 str(SOURCE), str(self.path)]
            )
        self.assertEqual(result, 0)
        self.assertTrue(self.path.is_file())
        self.assertEqual(self.path.stat().st_size, 0)

    def test_signal_waits_for_child_and_keeps_carrier(self):
        child_code = "import os,time; print(os.getpid(),flush=True); time.sleep(60)"
        code = IMPORT + "sys.exit(m.run([sys.executable,'-c',sys.argv[3]]))"
        process = subprocess.Popen(
            [sys.executable, "-c", code, str(SOURCE), str(self.path), child_code],
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
        )
        try:
            self.assertTrue(select.select([process.stdout], [], [], 5)[0])
            child_pid = int(process.stdout.readline())
            process.send_signal(signal.SIGTERM)
            self.assertEqual(process.wait(timeout=5), 143)
            with self.assertRaises(ProcessLookupError):
                os.kill(child_pid, 0)
            before = self.path.stat()
            fd = MODULE.acquire(self.path)
            os.close(fd)
            self.assertEqual(self.path.stat().st_ino, before.st_ino)
        finally:
            if process.poll() is None:
                process.kill()
                process.wait()
            process.stdout.close()
            process.stderr.close()


if __name__ == "__main__":
    unittest.main()
