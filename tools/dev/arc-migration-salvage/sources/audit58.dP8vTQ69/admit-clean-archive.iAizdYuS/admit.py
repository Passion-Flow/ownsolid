import pathlib
import shlex
import shutil
import subprocess
import sys
import tarfile

scratch, root = (pathlib.Path(p).resolve() for p in sys.argv[1:])
guard = (root / "guard.sh").read_text()

def git(*args):
    return subprocess.check_output(["git", *args], text=True).strip()

def pack_latest():
    subprocess.run(["git", "fetch", "origin"], check=True)
    base = git("rev-parse", "origin/main")
    candidate = git("rev-parse", "codex/audit58-catalog")
    tree = git("merge-tree", "--write-tree", base, candidate)
    (root / "revision").write_text(f"{base} + {candidate} (tree {tree})\n")
    own_files = ("revision", "build-offline.sh")
    fixture_files = ("replay-active.sh", "verify-active.sh", "catalog.sql",
                     "post-arc-restore.log", "audit_candidate_restore_matrix_test.go")
    with tarfile.open(root / "candidate.tar.gz", "w:gz") as output:
        for name in own_files:
            output.add(root / name, arcname=name)
        for name in fixture_files:
            output.add(scratch / name, arcname=name)
        with subprocess.Popen(["git", "archive", "--prefix=repo/", tree], stdout=subprocess.PIPE) as archive:
            with tarfile.open(fileobj=archive.stdout, mode="r|") as tracked:
                for member in tracked:
                    output.addfile(member, tracked.extractfile(member) if member.isfile() else None)
            if archive.wait() != 0:
                raise RuntimeError("Could not archive the candidate tree")
    print((root / "revision").read_text(), end="", flush=True)
    return root / "candidate.tar.gz"

with subprocess.Popen(["ssh", "-o", "BatchMode=yes", "ownsolid-cpu", f"bash -c {shlex.quote(guard)}"],
                      stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.STDOUT) as ssh:
    try:
        for line in iter(ssh.stdout.readline, b""):
            print(line.decode(errors="replace"), end="", flush=True)
            if line == b"Exclusive Linux admission acquired\n":
                with pack_latest().open("rb") as payload:
                    shutil.copyfileobj(payload, ssh.stdin)
                ssh.stdin.close()
    finally:
        ssh.stdin.close()
    sys.exit(ssh.wait())
