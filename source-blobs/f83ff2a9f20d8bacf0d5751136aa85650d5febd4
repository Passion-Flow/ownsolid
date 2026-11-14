"""Run the authorized file-only SDK attempt; never enter a native heavy gate."""
import ast
import ctypes
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import shlex
import signal
import subprocess
import tarfile
import time

os.umask(0o077)
r = Path(__file__).resolve().parent
w = r / "source"
plan = json.loads((r / "sdk-generation-plan.json").read_text())
started = time.monotonic()
proof = {"source": plan["source"], "tree": plan["tree"], "scope": "file-only SDK generation",
         "absolute_seconds_cap": 300, "per_command_seconds_cap": 60, "cpu_limit": 1,
         "heavy_carriers_opened": False, "native_tests_run": False, "full_gates_run": False,
         "external_generators_started": False, "admitted": False, "commands": []}


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def git(*args):
    return subprocess.check_output(["git", "-c", "core.commitGraph=false", *args],
                                   cwd=w, timeout=10).decode().strip()


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def reap():
    while True:
        try:
            pid, _ = os.waitpid(-1, os.WNOHANG)
        except ChildProcessError:
            return
        if not pid:
            return


def children():
    return [int(x) for x in Path(f"/proc/self/task/{os.getpid()}/children").read_text().split()]


def deadline(signum, frame):
    raise TimeoutError("SDK attempt reached its execution deadline")


def command(argv, env):
    item = {"command": argv, "started_seconds": round(time.monotonic() - started, 3)}
    proof["commands"].append(item)
    with (r / "generation.log").open("xb") as log:
        child = subprocess.Popen(argv, cwd=w, env=env, start_new_session=True,
                                 stdin=subprocess.DEVNULL, stdout=log, stderr=subprocess.STDOUT)
        item["pgid"] = child.pid
        try:
            item["exit"] = child.wait(timeout=55)
        except subprocess.TimeoutExpired:
            item["timed_out"] = True
            item["exit"] = 124
        finally:
            cleanup_end = time.monotonic() + 4
            while True:
                for pid in children():
                    try:
                        os.kill(pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                try:
                    os.killpg(child.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                reap()
                try:
                    os.killpg(child.pid, 0)
                    group_alive = True
                except ProcessLookupError:
                    group_alive = False
                if not group_alive and not children():
                    break
                if time.monotonic() >= cleanup_end:
                    raise RuntimeError("SDK command descendants did not clear within command budget")
                time.sleep(0.02)
            item["group_cleared"] = True
            item["owned_children_remaining"] = 0
    item["finished_seconds"] = round(time.monotonic() - started, 3)
    if item["exit"]:
        raise RuntimeError(f"SDK generator exited {item['exit']}")


class ResourceRefused(Exception):
    pass


exit_code = 1
signal.signal(signal.SIGALRM, deadline)
signal.setitimer(signal.ITIMER_REAL, 240)
with (r / "attempt.json").open("x") as f:
    json.dump({"source": plan["source"], "pid": os.getpid(), "attempt_consumed": True}, f)
try:
    if ctypes.CDLL(None, use_errno=True).prctl(36, 1, 0, 0, 0):
        raise RuntimeError("Cannot reap owned descendants")
    assert git("rev-parse", "HEAD") == plan["source"]
    assert git("rev-parse", "HEAD^{tree}") == plan["tree"]
    assert git("status", "--porcelain") == ""
    assert sha(w / "tools/codegen/sdk/generate.py") == plan["generator_sha256"]
    for path, expected in plan["contracts_sha256"].items():
        assert sha(w / path) == expected, path
    existing = Path("/root/lane-rel-tools/api-compat/full_native_gate.py")
    assert sha(existing) == plan["heavy_entry_sha256"]
    # Reuse the existing strict preflight verbatim, without executing its heavy entry.
    parsed = ast.parse(existing.read_text())
    functions = [n for n in parsed.body if isinstance(n, ast.FunctionDef)
                 and n.name in {"resources", "node_options"}]
    assert len(functions) == 2
    for fn in functions:
        if fn.name == "node_options":
            for node in ast.walk(fn):
                if isinstance(node, ast.Constant) and node.value == 4096:
                    node.value = 512
    exec(compile(ast.fix_missing_locations(ast.Module(body=functions, type_ignores=[])),
                 str(existing), "exec"), globals())
    proof["strict_preflight_source_sha256"] = sha(existing)
    resources()
    proof["admitted"] = True
    os.sched_setaffinity(0, {min(os.sched_getaffinity(0))})
    proof["cpu_affinity"] = sorted(os.sched_getaffinity(0))
    prepared = Path("/root/lane-rel-tools/obs109-contract.Psjaj7Gp")
    go = prepared / "bin/oapi-codegen"
    python = prepared / "python/bin/openapi-python-client"
    assert sha(go) == plan["go_binary_sha256"]
    assert sha(python) == plan["python_launcher_sha256"]
    assert sha(r / "base_generate_2e64.py") == plan["base_generator_sha256"]
    current = load("current_sdk", w / "tools/codegen/sdk/generate.py")
    base = load("base_sdk", r / "base_generate_2e64.py")
    # Every raw contract is byte-identical to the executed 2e source (plan DAG check).
    base.ROOT, base.CONTRACTS, base.TEMPLATES = w, current.CONTRACTS, current.TEMPLATES
    inputs = {}
    for source in current.public_sources(current.CONTRACTS):
        document, aliases = current.read_contract(source)
        projected = current.project_contract(document)
        if not projected["paths"]:
            continue
        old, old_aliases = base.read_contract(source)
        assert document == old and aliases == old_aliases
        raw = json.dumps(projected, sort_keys=True).encode()
        assert raw == json.dumps(base.project_contract(old), sort_keys=True).encode()
        prior = hashlib.sha256(json.dumps(projected, indent=2).encode()).hexdigest()
        assert prior == plan["prior_projected_sha256"][source.parent.name]
        inputs[source.parent.name] = {"source_path": str(source.relative_to(w)),
            "source_sha256": sha(source), "actual_generator_input_sha256": hashlib.sha256(raw).hexdigest(),
            "prior_projected_sha256": prior, "base_2e_projected_bytes_identical": True,
            "operations": sum(len(current.METHODS.intersection(v)) for v in projected["paths"].values())}
    assert len(inputs) == 28 and sum(x["operations"] for x in inputs.values()) == 430
    proof["domain_inputs"] = inputs
    env = os.environ.copy()
    env.update(PYTHONDONTWRITEBYTECODE="1", PIP_NO_INDEX="1", PIP_DISABLE_PIP_VERSION_CHECK="1",
               GOMAXPROCS="1", GOFLAGS="-p=1", GOPROXY="off", GOSUMDB="off", GOTOOLCHAIN="local",
               OMP_NUM_THREADS="1", OPENBLAS_NUM_THREADS="1", RAYON_NUM_THREADS="1")
    env["NODE_OPTIONS"] = node_options(env.get("NODE_OPTIONS", ""))
    proof["node_heap_cap_mib"] = int(env["NODE_OPTIONS"].rsplit("=", 1)[1])
    out = r / "sdk"
    assert not out.exists()
    argv = ["/usr/bin/unshare", "-n", str(prepared / "python/bin/python"),
            str(w / "tools/codegen/sdk/generate.py"), "--out", str(out),
            "--go-generator", str(go), "--python-generator", str(python)]
    proof["generator_command_started"] = True
    command(argv, env)
    proof["external_generators_started"] = True
    files = {str(p.relative_to(out)): sha(p) for p in sorted(out.rglob("*")) if p.is_file()}
    assert len(list((out / "gen/go").glob("*/client.gen.go"))) == 28
    archive = Path("/root/lane-rel-tools/identity-audit-contract/identity-sdk-inputs.tar.gz")
    assert sha(archive) == plan["old_identity_archive_sha256"]
    comparison = {"byte_identical": [], "changed": [], "missing": []}
    with tarfile.open(archive) as tar:
        previous = json.load(tar.extractfile("generation.json"))
        for name, expected in previous["file_sha256"].items():
            old_bytes = tar.extractfile("sdk/" + name).read()
            assert hashlib.sha256(old_bytes).hexdigest() == expected
            path = out / name
            category = "missing" if not path.exists() else "byte_identical" if path.read_bytes() == old_bytes else "changed"
            comparison[category].append(name)
    identity_scope = {name for name in files if name.startswith(("gen/go/identity/", "gen/python/ownsolid_sdk/identity/"))
                      or name in previous["file_sha256"]}
    comparison["new_identity_scope_files"] = sorted(identity_scope - previous["file_sha256"].keys())
    proof.update(generation_passed=True, generated_files=len(files), file_sha256=files,
                 old_identity_122_comparison=comparison, old_identity_archive_sha256=sha(archive),
                 base_2e_all_28_generated_output_comparison="NOTRUN: no retained full 28-domain output",
                 focused_tests_reference=plan["focused_tests_reference"], prior_compatibility_audit_exit=1,
                 source_clean_after=git("status", "--porcelain") == "", generation_log_sha256=sha(r / "generation.log"))
    exit_code = 0
except ResourceRefused as exc:
    proof.update(resource_refusal=True, error=str(exc))
    exit_code = 75
except Exception as exc:
    proof["error"] = f"{type(exc).__name__}: {exc}"
    if proof.get("generator_command_started") and not proof["external_generators_started"]:
        proof["external_generators_started"] = "UNKNOWN: inspect the bounded command log"
finally:
    signal.setitimer(signal.ITIMER_REAL, 0)
    proof.update(exit=exit_code, elapsed_seconds=round(time.monotonic() - started, 3),
                 owned_children_remaining=children(), output_exists=(r / "sdk").exists())
    with (r / "result.json").open("x") as f:
        json.dump(proof, f, indent=2)
        f.write("\n")
    print(json.dumps({k: v for k, v in proof.items() if k not in {"file_sha256", "domain_inputs"}}))
raise SystemExit(exit_code)
