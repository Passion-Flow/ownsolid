"""One authorized Mac tool preparation and SDK source-generation workload."""
import ast
import base64
import contextlib
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import re
import resource
import shutil
import signal
import stat
import subprocess
import sys
import tarfile
import tempfile
import time
import types

os.umask(0o077)
r = Path(__file__).resolve().parent
config = json.loads((r / "config.json").read_text())
launch = json.loads((r / "launch.json").read_text())
w = Path(config["worktree"])
deadline = launch["started_monotonic"] + 525
tool_deadline = launch["started_monotonic"] + 300
proof = {"source": config["source"], "tree": config["tree"], "commands": [], "modules": [],
         "generation": {}, "owned_groups": [], "native_or_full_run": False,
         "cpu_control": "serial commands, Go -p=1/GOMAXPROCS=1; no hard Darwin CPU affinity",
         "new_hard_rss_cap": False, "old_430_audit_exit": 1}
active = None
phase = "preparation"


def identity():
    current = os.lstat(config["carrier"])
    held = os.fstat(window_fd)
    for x in (current, held):
        if not (x.st_dev == 16777231 and x.st_ino == 53377467 and x.st_uid == 501 and stat.S_ISREG(x.st_mode) and stat.S_IMODE(x.st_mode) == 0o600 and x.st_nlink == 1 and x.st_size == 0):
            raise RuntimeError("Mac carrier identity changed")


def sha(path):
    return hashlib.sha256(Path(path).read_bytes()).hexdigest()


def expired(signum, frame):
    raise TimeoutError("Authorized execution budget expired; cleanup reserve retained")


def resources(label):
    out = subprocess.check_output(["/usr/bin/vm_stat"], text=True, timeout=5)
    page = int(re.search(r"page size of (\d+) bytes", out)[1])
    free = int(re.search(r"Pages free:\s+(\d+)", out)[1]) * page
    disk = os.statvfs(r)
    item = {"phase": label, "free_bytes": free, "disk_free_bytes": disk.f_bavail * disk.f_frsize}
    proof.setdefault("resource_snapshots", []).append(item)
    if free < 2048 * 1024**2 or item["disk_free_bytes"] < 15 * 1024**3:
        raise RuntimeError("Light workload resource threshold refused")


def stop_group(pgid):
    end = time.monotonic() + 6
    while True:
        try:
            os.killpg(pgid, 0)
        except ProcessLookupError:
            return
        try:
            os.killpg(pgid, signal.SIGKILL)
        except ProcessLookupError:
            return
        if time.monotonic() >= end:
            raise RuntimeError("Owned process group did not clear within cleanup budget")
        time.sleep(0.04)


def run(argv, *, seconds=50, stdout=None, capture_output=False, text=False, check=True, env=None):
    global active
    identity()
    if active is not None:
        raise RuntimeError("Commands must remain serial")
    available = min(deadline, tool_deadline if phase.startswith("preparation") else deadline) - time.monotonic() - 8
    if available <= 0:
        raise TimeoutError("No command budget remains")
    item = {"phase": phase, "argv": list(argv), "timeout_seconds": min(seconds, available)}
    index = len(proof["commands"]) + 1
    out_path, err_path = r / f"command-{index:03d}.stdout", r / f"command-{index:03d}.stderr"
    item.update(stdout_log=str(out_path), stderr_log=str(err_path))
    proof["commands"].append(item)
    print(json.dumps({"command": index, "phase": phase, "argv": argv[:4]}), flush=True)
    target = stdout if stdout is not None and stdout not in (sys.stdout, sys.stderr) else None
    profile = r / ("native.sb" if phase == "native" else "download.sb" if phase == "preparation-download" else "build.sb" if phase == "preparation" else "generation.sb")
    started = time.monotonic()
    with out_path.open("xb") as output, err_path.open("xb") as errors:
        active = subprocess.Popen(["/usr/bin/sandbox-exec", "-f", str(profile), *argv],
                                  cwd=native_cwd if phase == "native" else r / ("build" if phase.startswith("preparation") else "metadata-module"),
                                  env=env or environment, stdin=subprocess.DEVNULL,
                                  stdout=target or output, stderr=errors, start_new_session=True,
                                  pass_fds=(window_fd,))
        group = {"pgid": active.pid, "cleared": False}
        proof["owned_groups"].append(group)
        item["pgid"] = active.pid
        peak = 0
        try:
            next_sample = 0
            while True:
                pid, status, usage = os.wait4(active.pid, os.WNOHANG)
                if pid:
                    active.returncode = os.waitstatus_to_exitcode(status)
                    item.update(exit=active.returncode, wait4_maxrss_bytes=usage.ru_maxrss)
                    break
                identity()
                now = time.monotonic()
                if now - started >= item["timeout_seconds"]:
                    raise TimeoutError("Command deadline reached")
                if now >= next_sample:
                    ps = subprocess.check_output(["/bin/ps", "-axo", "pgid=,rss="], text=True, timeout=3)
                    peak = max(peak, sum(int(parts[1]) * 1024 for line in ps.splitlines()
                                        if len(parts := line.split()) == 2 and int(parts[0]) == active.pid))
                    next_sample = now + 0.5
                time.sleep(0.03)
        finally:
            if active.returncode is None:
                try:
                    os.killpg(active.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                _, status, usage = os.wait4(active.pid, 0)
                active.returncode = os.waitstatus_to_exitcode(status)
                item.update(exit=active.returncode, wait4_maxrss_bytes=usage.ru_maxrss)
            stop_group(active.pid)
            identity()
            group["cleared"] = True
            active = None
            item.update(elapsed_seconds=round(time.monotonic() - started, 3),
                        sampled_group_peak_rss_bytes=peak, rss_sample_interval_seconds=0.5)
    item.update(stdout_sha256=sha(out_path), stderr_sha256=sha(err_path))
    result = subprocess.CompletedProcess(argv, item["exit"], out_path.read_text() if text else out_path.read_bytes(),
                                         err_path.read_text() if text else err_path.read_bytes())
    if check and result.returncode:
        raise subprocess.CalledProcessError(result.returncode, argv)
    return result


def verify_inputs():
    for name, expected in config["source_inputs"].items():
        assert sha(w / name) == expected, name


def h1_file(path):
    return "h1:" + base64.b64encode(hashlib.sha256((sha(path) + "  go.mod\n").encode()).digest()).decode()


def verify_modules():
    path = Path(config["tool_verifier_path"])
    assert sha(path) == config["tool_verifier_sha256"]
    verifier = load("pinned_tool_module_verifier", path)
    proof["modules"] = verifier.verify_cached_modules(Path("/Users/passion./go/pkg/mod"), config["modules"])
    assert len(proof["modules"]) == 23
    proof["module_verifier_source"] = config["tool_verifier_source"]
    proof["module_verifier_sha256"] = config["tool_verifier_sha256"]


def load(name, path):
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


@contextlib.contextmanager
def preserving_temporary_directory(**kwargs):
    path = tempfile.mkdtemp(**kwargs)
    try:
        yield path
    except BaseException:
        proof.setdefault("retained_partial_directories", []).append(path)
        raise
    else:
        shutil.rmtree(path)


def generation_run(argv, **kwargs):
    if argv[0] == str(go_generator):
        assert argv[1:] == ["-version"] or len(argv) == 4 and argv[1] == "-config"
    elif argv[0] == config["python_launcher"]["path"]:
        assert argv[1:] == ["--version"] or argv[1] == "generate"
        argv = [config["python_command"], *argv]
    else:
        raise RuntimeError("Unexpected generator executable")
    return run(argv, **kwargs)


def files(root):
    return {str(p.relative_to(root)): {"bytes": p.stat().st_size, "sha256": sha(p)}
            for p in sorted(root.rglob("*")) if p.is_file()}


def generate(module, label):
    resources("before-" + label)
    verify_inputs()
    module.ROOT, module.CONTRACTS = w, w / "api/openapi/v1"
    module.TEMPLATES = w / "tools/codegen/sdk/templates"
    module.subprocess = types.SimpleNamespace(run=generation_run, CalledProcessError=subprocess.CalledProcessError)
    module.tempfile = types.SimpleNamespace(TemporaryDirectory=preserving_temporary_directory)
    destination = r / (label + "-sdk")
    count = module.generate(destination, str(go_generator), config["python_launcher"]["path"])
    listing = files(destination)
    proof["generation"][label] = {"domains": count, "files": len(listing), "file_manifest": listing,
                                   "generator_sha256": sha(Path(module.__file__)), "output": str(destination)}
    assert count == 28
    return destination, listing


signal.signal(signal.SIGALRM, expired)
signal.signal(signal.SIGTERM, expired)
signal.signal(signal.SIGINT, expired)
signal.setitimer(signal.ITIMER_REAL, max(0.1, deadline - time.monotonic()))
window_fd = int(os.environ["OWNSOLID_MAC_WINDOW_FD"])
carrier = Path(config["carrier"])
opened = os.fstat(window_fd)
proof["worker_carrier_fd"] = {"pid": os.getpid(), "fd": window_fd, "role": "inherited duplicate of the parent-held carrier open-file description", "device": opened.st_dev, "inode": opened.st_ino}
assert opened.st_ino == config["carrier_inode"] and opened.st_ino == carrier.stat().st_ino
environment = os.environ.copy()
exit_code = 1
try:
    resources("after-carrier")
    verify_inputs()
    for field in ["go", "python", "python_launcher"]:
        assert sha(config[field]["path"]) == config[field]["sha256"]
    for path, expected in config["python_package_files"].items():
        assert sha(Path(config["python_package_root"]) / path) == expected
    raw_limit = environment.get("GOMEMLIMIT", "512MiB")
    inherited_flags = environment.get("GOFLAGS", "").split()
    if any(not (flag.startswith("-p=") or flag == "-mod=readonly") for flag in inherited_flags):
        raise ValueError("Unsupported inherited Go build flags; no flags discarded")
    match = re.fullmatch(r"(\d+)(B|KiB|MiB|GiB|TiB)?", raw_limit)
    if raw_limit == "off":
        soft_limit = 512 * 1024**2
    elif match:
        soft_limit = min(512 * 1024**2, int(match[1]) * {None: 1, "B": 1, "KiB": 1024, "MiB": 1024**2, "GiB": 1024**3, "TiB": 1024**4}[match[2]])
    else:
        raise ValueError("Unsupported inherited Go soft memory limit")
    environment.update(GOMAXPROCS="1", GOMEMLIMIT=str(soft_limit) + "B", GOFLAGS="-p=1 -mod=readonly",
                       GOTOOLCHAIN="local", GOVCS="*:off", CGO_ENABLED="0", GOOS="darwin", GOARCH="arm64",
                       GOWORK="off", GOMODCACHE="/Users/passion./go/pkg/mod",
                       GOCACHE="/Users/passion./Library/Caches/go-build", GOBIN=str(r / "bin"),
                       GOPROXY="off", GOSUMDB="off", GOINSECURE="", GOPRIVATE="", GONOPROXY="", GONOSUMDB="",
                       PYTHONDONTWRITEBYTECODE="1", PIP_NO_INDEX="1", RAYON_NUM_THREADS="1")
    environment["PYTHONPATH"] = str(Path(config["python_package_root"]).parent)
    environment["PYTHONNOUSERSITE"] = "1"
    proof["effective_go_soft_limit_bytes"] = soft_limit
    proof["inherited_rlimits"] = {name: list(resource.getrlimit(getattr(resource, name)))
                                  for name in ["RLIMIT_AS", "RLIMIT_DATA", "RLIMIT_RSS", "RLIMIT_CPU"]}
    for directory in ["bin", "build", "metadata-module", "metadata-bin", "tmp", "telemetry"]:
        (r / directory).mkdir(mode=0o700)
    (r / "telemetry/mode").write_text("off\n")
    environment["TEST_TELEMETRY_DIR"] = str(r / "telemetry")
    proof["tool_telemetry"] = "Disabled in a private mode file using the pinned Go source's TEST_TELEMETRY_DIR setting"
    shutil.copyfile(r / "metadata-go.py", r / "metadata-bin/go")
    (r / "metadata-bin/go").chmod(0o700)
    environment.update(TMPDIR=str(r / "tmp") + "/", GOTMPDIR=str(r / "tmp"))
    (r / "build/go.mod").write_text("module ownsolid.private.tool.preparation\n\ngo 1.27.3\n")
    print(json.dumps({"phase": "tool-preparation", "planned_downloads": len(config["planned_downloads"])}), flush=True)
    for item in config["planned_downloads"]:
        cache_root = Path("/Users/passion./go/pkg/mod") / (item["module"] + "@" + item["version"])
        if cache_root.is_dir():
            continue
        phase = "preparation-download"
        online = {**environment, "GOPROXY": "https://proxy.golang.org", "GOSUMDB": "sum.golang.org"}
        result = run([config["go"]["path"], "mod", "download", "-json", item["module"] + "@" + item["version"]],
                     seconds=min(40, tool_deadline - time.monotonic() - 20), text=True, env=online)
        report = json.loads(result.stdout)
        assert report["Sum"] == item["sum"] and report["GoModSum"] == item["go_mod_sum"]
    phase = "preparation"
    verify_modules()
    resources("before-tool-build")
    tool_environment = {**environment,
                        "GOPROXY": Path("/Users/passion./go/pkg/mod/cache/download").as_uri(),
                        "GOMODCACHE": str(r / "tool-module-cache")}
    run([config["go"]["path"], "install", "github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.2"],
        seconds=min(270, tool_deadline - time.monotonic() - 18), env=tool_environment)
    go_generator = r / "bin/oapi-codegen"
    header = go_generator.read_bytes()[:8]
    assert header[:4] == bytes.fromhex("cffaedfe") and int.from_bytes(header[4:8], "little") == 0x100000c
    tool_version = run([str(go_generator), "-version"], text=True, seconds=10).stdout.strip()
    assert tool_version.split()[-1] == "v2.8.2" and "(devel)" not in tool_version
    proof["prepared_tool"] = {"path": str(go_generator), "sha256": sha(go_generator), "version_output": tool_version,
                               "GOOS": "darwin", "GOARCH": "arm64", "module_source_h1": "h1:68UGCFId8tysEuz1jqDgem0Lnz0d6T+73br+KwH/W6k="}
    proof["prepared_tool"]["buildinfo"] = run([config["go"]["path"], "version", "-m", str(go_generator)], seconds=10, text=True).stdout
    assert "\tv2.8.2\t" in proof["prepared_tool"]["buildinfo"]
    expected_pins = {pin["module"]: pin for pin in config["modules"]}
    for line in proof["prepared_tool"]["buildinfo"].splitlines():
        fields = line.split()
        if fields and fields[0] in {"mod", "dep"}:
            pin = expected_pins[fields[1]]
            assert fields[2] == pin["version"] and fields[3] == pin["sum"], fields[1]

    print(json.dumps({"phase": "tool-ready", **proof["prepared_tool"]}), flush=True)
    phase = "generation"
    environment["PATH"] = str(r / "metadata-bin") + ":" + str(Path(config["go"]["path"]).parent) + ":/usr/bin:/bin"
    for name in ["go.mod", "go.sum"]:
        shutil.copyfile(w / "tools/codegen/sdk/templates" / (name + ".template"), r / "metadata-module" / name)
        (r / "metadata-module" / name).chmod(0o444)
    current = load("current_sdk", w / "tools/codegen/sdk/generate.py")
    current.subprocess = types.SimpleNamespace(run=generation_run)
    python_version = current.checked_tool(config["python_launcher"]["path"], "--version", "0.29.1")
    assert python_version == config["python_launcher"]["path"]
    print(json.dumps({"phase": "current-28-domain-generation"}), flush=True)
    new_root, new = generate(current, "current")
    assert sha(r / "baseline_generate.py") == config["baseline_generator_sha256"]
    baseline = load("baseline_sdk", r / "baseline_generate.py")
    print(json.dumps({"phase": "baseline-2e-28-domain-generation"}), flush=True)
    base_root, old = generate(baseline, "baseline-2e")
    changed = [p for p in new.keys() & old.keys() if (new_root / p).read_bytes() != (base_root / p).read_bytes()]
    proof["full_28_comparison"] = {"byte_identical": sorted((new.keys() & old.keys()) - set(changed)), "changed": sorted(changed),
                                   "new_only": sorted(new.keys() - old.keys()), "baseline_only": sorted(old.keys() - new.keys())}
    archive = Path(config["old_archive"])
    assert sha(archive) == config["old_archive_sha256"]
    comparison = {"byte_identical": [], "changed": [], "missing": []}
    with tarfile.open(archive) as tar:
        previous = json.load(tar.extractfile("generation.json"))
        for name, expected in previous["file_sha256"].items():
            content = tar.extractfile("sdk/" + name).read()
            assert hashlib.sha256(content).hexdigest() == expected
            path = new_root / name
            key = "missing" if not path.exists() else "byte_identical" if path.read_bytes() == content else "changed"
            comparison[key].append(name)
    proof["old_identity_122_comparison"] = comparison
    verify_inputs()
    assert sha(go_generator) == proof["prepared_tool"]["sha256"]
    proof["source_inputs_36_unchanged_after"] = True
    proof["generation_completed"] = True
    proof["valid_input_generated_bytes_identical_to_2e"] = not changed and new.keys() == old.keys()

    proof["native_groups"] = {name: {"state": "NOTRUN"} for name in ["old-caller-old-sdk", "old-caller-new-sdk", "new-explicit-caller-new-sdk", "client-rollback"]}
    if deadline - time.monotonic() > 80:
        phase = "generation"
        old_source = Path(config["old_baseline_root"])
        legacy = load("legacy_sdk", old_source / "tools/codegen/sdk/generate.py")
        legacy.subprocess = types.SimpleNamespace(run=generation_run, CalledProcessError=subprocess.CalledProcessError)
        old_root = r / "baseline-9e-sdk"
        temp = r / "baseline-9e-temp"
        temp.mkdir()
        go_root, py_root = old_root / "gen/go", old_root / "gen/python/ownsolid_sdk"
        go_root.mkdir(parents=True)
        py_root.mkdir(parents=True)
        assert legacy.generate_domain(old_source / "api/openapi/v1/identity/openapi.json", temp, go_root, py_root, str(go_generator), config["python_launcher"]["path"])
        for filename in ["go.mod", "go.sum"]:
            shutil.copyfile(old_source / "tools/codegen/sdk/templates" / (filename + ".template"), go_root / filename)
        proof["old_baseline_identity"] = {"source": "9e32b023a0a3deed6a8653a8878d52c2583fd6cf", "output": str(old_root), "files": files(old_root), "scope": "one original identity domain"}
        tests = Path(config["fixture_root"]) / "tools/codegen/sdk/tests"
        native_env = {**environment, "CGO_ENABLED": "1", "PATH": str(Path(config["go"]["path"]).parent) + ":/usr/bin:/bin", "GOPROXY": "off", "GOSUMDB": "off"}
        for label, sdk, fixture in [("old-caller-old-sdk", old_root, "audit_read_legacy_test.go"), ("old-caller-new-sdk", new_root, "audit_read_legacy_test.go"), ("new-explicit-caller-new-sdk", new_root, "audit_read_wire_test.go"), ("client-rollback", old_root, "audit_read_legacy_test.go")]:
            if deadline - time.monotonic() < 35:
                break
            phase = "native"
            resources("before-" + label)
            native_cwd = sdk / "gen/go"
            result = run([config["go"]["path"], "test", "-mod=readonly", "-p=1", "-race", "-count=1", "-json", str(tests / fixture)], seconds=min(120, deadline - time.monotonic() - 12), env=native_env, text=True, check=False)
            events = [json.loads(line) for line in result.stdout.splitlines() if line.startswith("{")]
            passed = [x["Test"] for x in events if x.get("Action") == "pass" and "Test" in x]
            skipped = [x["Test"] for x in events if x.get("Action") == "skip" and "Test" in x]
            failed = [x.get("Test") for x in events if x.get("Action") == "fail"]
            proof["native_groups"][label] = {"state": "RUN", "exit": result.returncode, "passed_cases": passed, "skipped_cases": skipped, "failed_cases": failed}
            if result.returncode:
                break
        proof["native_or_full_run"] = any(x["state"] == "RUN" for x in proof["native_groups"].values())
    proof["generation_file_only_rss_note"] = "Per-command wait4/group RSS retained; no new hard RSS bound"
    exit_code = 0
except BaseException as error:
    proof["error"] = type(error).__name__ + ": " + str(error)
finally:
    signal.setitimer(signal.ITIMER_REAL, 0)
    if active is not None:
        try:
            os.killpg(active.pid, signal.SIGKILL)
            active.wait(timeout=5)
            stop_group(active.pid)
        except ProcessLookupError:
            pass
    proof.update(exit=exit_code, elapsed_seconds=round(time.monotonic() - launch["started_monotonic"], 3),
                 parent_peak_rss_bytes=resource.getrusage(resource.RUSAGE_SELF).ru_maxrss,
                 all_owned_groups_cleared=all(x["cleared"] for x in proof["owned_groups"]))
    os.close(window_fd)
    try:
        os.fstat(window_fd)
        proof["worker_window_fd_closed"] = False
    except OSError:
        proof["worker_window_fd_closed"] = True
    with (r / "worker-result.json").open("x") as output:
        json.dump(proof, output, indent=2)
        output.write("\n")
    print(json.dumps({k: v for k, v in proof.items() if k not in {"commands", "modules", "generation", "full_28_comparison"}}), flush=True)
raise SystemExit(exit_code)
