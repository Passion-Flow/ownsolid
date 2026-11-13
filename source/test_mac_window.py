"""Exercise private carriers only; never acquire the shared OBS window."""
import fcntl
import os
from pathlib import Path
import stat
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

from with_mac_window import Refused, run


class MacWindowTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.lock = self.root / "permanent.lock"

    def command(self, code="pass"):
        return [sys.executable, "-c", code]

    def test_missing_file_is_created_and_retained(self):
        self.assertEqual(run(self.command(), self.lock), 0)
        self.assertTrue(stat.S_ISREG(self.lock.lstat().st_mode))
        self.assertEqual(self.lock.read_bytes(), b"")
        self.assertEqual(stat.S_IMODE(self.lock.stat().st_mode), 0o600)

    def test_existing_file_content_and_inode_are_preserved(self):
        self.lock.write_bytes(b"preserve this carrier")
        before = self.lock.stat()
        self.assertEqual(run(self.command("raise SystemExit(9)"), self.lock), 9)
        after = self.lock.stat()
        self.assertEqual((before.st_dev, before.st_ino), (after.st_dev, after.st_ino))
        self.assertEqual(self.lock.read_bytes(), b"preserve this carrier")
        with self.lock.open("r+b") as check:
            fcntl.flock(check, fcntl.LOCK_EX | fcntl.LOCK_NB)

    def test_busy_file_refuses_before_starting_command(self):
        self.lock.touch()
        marker = self.root / "not-started"
        with self.lock.open("r+b") as owner:
            fcntl.flock(owner, fcntl.LOCK_EX | fcntl.LOCK_NB)
            with self.assertRaises(Refused):
                run(self.command("from pathlib import Path; Path(" + repr(str(marker)) + ").touch()"), self.lock)
        self.assertFalse(marker.exists())
        self.assertTrue(self.lock.exists())

    def test_directory_carrier_is_left_untouched(self):
        self.lock.mkdir()
        sentinel = self.lock / "foreign-owner"
        sentinel.write_text("keep")
        with self.assertRaises(Refused):
            run(self.command(), self.lock)
        self.assertEqual(sentinel.read_text(), "keep")

    def test_symlink_carrier_is_refused_without_touching_target(self):
        target = self.root / "target"
        target.write_text("keep")
        self.lock.symlink_to(target)
        with self.assertRaises(Refused):
            run(self.command(), self.lock)
        self.assertTrue(self.lock.is_symlink())
        self.assertEqual(target.read_text(), "keep")

    def test_wrong_uid_is_refused(self):
        self.lock.touch()
        with patch("with_mac_window.os.geteuid", return_value=os.geteuid() + 1):
            with self.assertRaises(Refused):
                run(self.command(), self.lock)

    def test_command_observes_exclusive_lock_until_exit(self):
        code = """import fcntl,sys
with open(sys.argv[1], 'r+b') as competitor:
    try: fcntl.flock(competitor, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError: raise SystemExit(0)
    raise SystemExit(22)
"""
        self.assertEqual(run(self.command(code) + [str(self.lock)], self.lock), 0)
        with self.lock.open("r+b") as check:
            fcntl.flock(check, fcntl.LOCK_EX | fcntl.LOCK_NB)

    def test_bounded_kernel_wait_expires_without_starting_child(self):
        self.lock.touch()
        marker = self.root / "never-started"
        with self.lock.open("r+b") as owner:
            fcntl.flock(owner, fcntl.LOCK_EX | fcntl.LOCK_NB)
            with self.assertRaisesRegex(Refused, "wait expired"):
                run(self.command("from pathlib import Path; Path(" + repr(str(marker)) + ").touch()"),
                    self.lock, wait_seconds=0.03)
        self.assertFalse(marker.exists())

    def test_assigned_missing_carrier_is_not_created(self):
        with self.assertRaises(Refused):
            run(self.command(), self.lock, expected_inode=123)
        self.assertFalse(self.lock.exists())

    def test_bounded_child_is_joined_and_parent_closes_its_fd(self):
        self.lock.touch()
        proof = self.root / "proof.json"
        with self.assertRaises(subprocess.TimeoutExpired):
            run(self.command("import time; time.sleep(20)"), self.lock,
                execution_seconds=0.03, proof_path=proof)
        import json
        result = json.loads(proof.read_text())
        self.assertTrue(result["child_exited_before_close"])
        self.assertTrue(result["fd_closed"])
        self.assertTrue(result["carrier_preserved"])


if __name__ == "__main__":
    unittest.main(verbosity=2)
