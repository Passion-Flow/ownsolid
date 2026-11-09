"""Run the SCI assistant audit fixture offline against the AGT-112 candidate."""

import argparse
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time

FIXTURE = Path(
    "services/control/internal/modules/audit/assistant_disclosure_integration_test.go"
)
CONSTRUCTOR = (
    "productagent.New(&applications.Service{Identity: owner, Workspaces: spaces, Workflows: flow})"
)
ENABLE = "func(context.Context) (bool, error) { return true, nil }"
AUDIT_DIR = "/tmp/audit-fixture"
TEST_DIR = AUDIT_DIR + "/services/control/internal/modules/audit"
DSN_FILE = AUDIT_DIR + "/postgres.dsn"
DATABASE = "assistant_audit_test"


def run(*args, **kwargs):
    return subprocess.run(args, check=True, text=True, **kwargs)


def write_overlay(repo, scratch):
    fixture = repo / FIXTURE
    source = fixture.read_text(encoding="utf-8")
    if source.count(CONSTRUCTOR) != 1:
        raise SystemExit("Expected one legacy constructor in the SCI audit fixture")
    replacement = scratch / fixture.name
    replacement.write_text(
        source.replace(CONSTRUCTOR, CONSTRUCTOR[:-1] + f", {ENABLE})", 1),
        encoding="utf-8",
    )
    overlay = scratch / "overlay.json"
    overlay.write_text(
        json.dumps({"Replace": {str(fixture): str(replacement)}}) + "\n",
        encoding="utf-8",
    )
    return overlay


def build(repo, scratch, arch):
    overlay = write_overlay(repo, scratch)
    env = os.environ | {
        "CGO_ENABLED": "0",
        "GOOS": "linux",
        "GOARCH": arch,
        "GOPROXY": "off",
        "GOSUMDB": "off",
    }
    run(
        "go", "-C", "services/control", "test", "-p=1", "-overlay", str(overlay),
        "-c", "-o", str(scratch / "audit.test"), "./internal/modules/audit",
        cwd=repo, env=env,
    )
    run(
        "go", "build", "-p=1", "-trimpath", "-o", str(scratch / "audit-verify"),
        "./tools/audit-verify/", cwd=repo, env=env,
    )


def await_database(container):
    deadline = time.monotonic() + 45
    while time.monotonic() < deadline:
        ready = subprocess.run(
            ["docker", "exec", container, "pg_isready", "-q", "-h", "127.0.0.1",
             "-U", "postgres", "-d", DATABASE],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, timeout=5,
        )
        if ready.returncode == 0:
            return
        time.sleep(1)
    raise SystemExit("The isolated PostgreSQL fixture did not become ready")


def exercise(container, repo, scratch):
    await_database(container)
    run(
        "docker", "exec", container, "psql", "-X", "-U", "postgres", "-d", DATABASE,
        "-v", "ON_ERROR_STOP=1", "-c",
        "CREATE EXTENSION vector WITH SCHEMA public VERSION '0.8.7'; "
        "REVOKE CREATE ON SCHEMA public FROM PUBLIC; SHOW server_version;",
    )
    run(
        "docker", "exec", container, "install", "-d", "-m", "0700", TEST_DIR,
        AUDIT_DIR + "/tools/audit-verify",
    )
    run(
        "docker", "exec", "-i", container, "sh", "-c",
        'umask 077; cat > "$1"', "sh", DSN_FILE,
        input=f"postgresql://postgres@127.0.0.1:5432/{DATABASE}?sslmode=disable\n",
    )
    for source, target in (
        (scratch / "audit.test", AUDIT_DIR + "/audit.test"),
        (scratch / "audit-verify", AUDIT_DIR + "/audit-verify"),
        (repo / "tools/audit-verify/export.sql", AUDIT_DIR + "/tools/audit-verify/export.sql"),
    ):
        run("docker", "cp", str(source), f"{container}:{target}")
    run(
        "docker", "exec", "-w", TEST_DIR, "-e", "GOMAXPROCS=2",
        "-e", f"PROJECT_ONE_TEST_DSN_FILE={DSN_FILE}",
        "-e", f"OWNSOLID_TEST_AUDIT_VERIFY={AUDIT_DIR}/audit-verify",
        container, AUDIT_DIR + "/audit.test", "-test.v", "-test.parallel=1",
        "-test.timeout=180s", "-test.run", "^TestProductAssistantAuditWithin$",
    )


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("checkout", type=Path, help="checkout of the installation policy candidate")
    parser.add_argument("--image", default="pgvector/pgvector:0.8.7-pg18-bookworm",
                        help="locally available PostgreSQL 18.6 and pgvector 0.8.7 image")
    args = parser.parse_args()
    repo = args.checkout.resolve(strict=True)
    image, platform, arch = run(
        "docker", "image", "inspect", "--format", "{{.Id}} {{.Os}} {{.Architecture}}",
        args.image, capture_output=True,
    ).stdout.split()
    if platform != "linux" or arch not in {"amd64", "arm64"}:
        raise SystemExit("Expected a local Linux amd64 or arm64 fixture image")

    with tempfile.TemporaryDirectory(prefix="assistant-audit-") as directory:
        scratch = Path(directory).resolve()
        build(repo, scratch, arch)
        container = run(
            "docker", "run", "--rm", "--pull=never", "--network", "none", "-d",
            "-e", "POSTGRES_HOST_AUTH_METHOD=trust", "-e", f"POSTGRES_DB={DATABASE}",
            image, capture_output=True,
        ).stdout.strip()
        try:
            exercise(container, repo, scratch)
        finally:
            run("docker", "stop", "--time", "5", container, stdout=subprocess.DEVNULL)


if __name__ == "__main__":
    main()
