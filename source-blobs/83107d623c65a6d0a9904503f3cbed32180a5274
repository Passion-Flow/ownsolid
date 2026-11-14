"""Stage checked Linux amd64 tokenizer inputs for disconnected fixture builds."""

import argparse
from email import policy
from email.parser import BytesParser
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import platform
import re
import shutil
import stat
import subprocess
import sys
import zipfile

LOCK = "linux-amd64-cuda.lock"
DISTRIBUTIONS = frozenset("""
annotated-doc anyio click filelock fsspec h11 hf-xet httpcore2 httpx2
huggingface-hub idna jinja2 markdown-it-py markupsafe mdurl numpy packaging
pygments pyyaml regex rich safetensors setuptools shellingham tokenizers
tqdm transformers truststore typer typing-extensions
""".split())
MODEL_FILES = frozenset((
    "LICENSE", "config.json", "merges.txt", "tokenizer.json", "tokenizer_config.json", "vocab.json",
))
PIN = re.compile(r"([a-z0-9-]+)==([a-zA-Z0-9.+!]+)((?: +--hash=sha256:[0-9a-f]{64})+)")
WHEEL = re.compile(r"([A-Za-z0-9_]+)-([A-Za-z0-9_.+!]+)(?:-[0-9][A-Za-z0-9_]*)?"
                   r"-([a-z0-9.]+)-([a-z0-9.]+)-([a-z0-9_.]+)\.whl")
READ_FLAGS = os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK
PIP_FROM_WHEEL = (
    "import runpy, sys; sys.path.insert(0, sys.argv.pop(1)); "
    "runpy.run_module('pip', run_name='__main__', alter_sys=True)"
)


def require(condition, message):
    if not condition:
        raise ValueError(message)


def normalized(name):
    return re.sub(r"[-_.]+", "-", name.lower())


def project_lock(raw, expected):
    require(hashlib.sha256(raw).hexdigest() == expected, "Training lock SHA-256 mismatch")
    pins = {}
    lines = []
    for line in raw.decode("ascii").replace("\\\n", "").splitlines():
        match = PIN.fullmatch(line)
        require(match is not None, f"Invalid training lock entry: {line.partition(' ')[0]}")
        name, version, hashes = match.groups()
        if name in DISTRIBUTIONS:
            require(name not in pins, f"Duplicate tokenizer lock pin: {name}")
            pins[name] = (version, {word.rsplit(":", 1)[1] for word in hashes.split()})
            lines.append(line)
    require(pins.keys() == DISTRIBUTIONS, "Missing tokenizer pins: " + ", ".join(sorted(DISTRIBUTIONS - pins.keys())))
    return pins, ("\n".join(lines) + "\n").encode("ascii")


def read_contract(path):
    with os.fdopen(os.open(path, READ_FLAGS), "rb") as stream:
        mode = os.fstat(stream.fileno()).st_mode
        require(stat.S_ISREG(mode) and not mode & 0o022, f"Unprotected contract: {path.name}")
        return stream.read()


def copy_checked(source, destination, hashes, size=None):
    with os.fdopen(os.open(source, READ_FLAGS), "rb") as stream:
        info = os.fstat(stream.fileno())
        require(stat.S_ISREG(info.st_mode) and info.st_size > 0, f"Invalid regular file: {source.name}")
        require(size is None or info.st_size == size, f"Size mismatch: {source.name}")
        sha256 = hashlib.file_digest(stream, "sha256").hexdigest()
        require(sha256 in hashes, f"SHA-256 mismatch: {source.name}")
        stream.seek(0)
        with destination.open("xb") as output:
            shutil.copyfileobj(stream, output)
    with destination.open("rb") as output:
        actual = hashlib.file_digest(output, "sha256").hexdigest()
    require(actual == sha256 and destination.stat().st_size == info.st_size,
            f"Source changed during staging: {source.name}")
    return {"path": source.name, "size_bytes": info.st_size, "sha256": sha256}


def wheel_inventory(path, name, version):
    with zipfile.ZipFile(path) as wheel:
        files = wheel.namelist()
        metadata = [item for item in files if item.endswith(".dist-info/METADATA") and item.count("/") == 1]
        require(len(metadata) == 1, f"Missing or ambiguous wheel metadata: {path.name}")
        info = BytesParser(policy=policy.default).parsebytes(wheel.read(metadata[0]), headersonly=True)
        require(normalized(info.get("Name", "")) == name and info.get("Version") == version,
                f"Wheel metadata identity mismatch: {path.name}")
        root = PurePosixPath(metadata[0]).parent
        notices = {}
        # Older wheels put notices directly in dist-info. Vendored libraries may carry their own.
        for item in sorted(files):
            entry = PurePosixPath(item)
            if item.endswith("/") or not (
                entry.is_relative_to(root / "licenses")
                or re.match(r"^(licen[cs]e|notice|copying|copyright)(?:[._-]|$)", entry.name, re.I)
            ):
                continue
            require(item not in notices, f"Duplicate notice in {path.name}: {item}")
            with wheel.open(item) as stream:
                notices[item] = hashlib.file_digest(stream, "sha256").hexdigest()
        return {"name": name, "version": version, "license_expression": info.get("License-Expression"),
                "license": info.get("License"), "license_classifiers": [
                    value for value in info.get_all("Classifier", []) if value.startswith("License ::")
                ], "notices": notices}


def wheelhouse(source, output, runtime, pins):
    manufacturing = runtime["linux_manufacturing"]
    installer = manufacturing["image_installer"]
    pins = dict(pins, pip=(installer["version"], {installer["sha256"]}))
    allowed_platforms = set(manufacturing["platforms"]) | {"any"}
    allowed_abis = set(manufacturing["abis"])
    wheels = {}
    output.mkdir()
    for path in sorted(source.iterdir()):
        match = WHEEL.fullmatch(path.name)
        require(match is not None, f"Unexpected wheelhouse input: {path.name}")
        distribution, version, _, abi, target = match.groups()
        name = normalized(distribution)
        require(name in pins and name not in wheels and version == pins[name][0],
                f"Unexpected or duplicate wheel pin: {path.name}")
        require(allowed_platforms.intersection(target.split(".")) and allowed_abis.intersection(abi.split(".")),
                f"Incompatible Linux amd64 wheel: {path.name}")
        size = None
        if name == "pip":
            require(path.name == installer["filename"], f"Unexpected pip wheel: {path.name}")
            size = installer["size_bytes"]
        staged = output / path.name
        identity = copy_checked(path, staged, pins[name][1], size)
        wheels[name] = dict(identity, **wheel_inventory(staged, name, version))
    missing_wheels = sorted(pins.keys() - wheels.keys())
    missing_notices = sorted(name for name, wheel in wheels.items() if not wheel["notices"])
    require(not (missing_wheels or missing_notices),
            f"Incomplete wheelhouse (missing wheels: {', '.join(missing_wheels) or 'none'}; "
            f"missing notices: {', '.join(missing_notices) or 'none'})")
    return wheels


def install(output, runtime, wheels):
    limits = runtime["manufacturing"]
    env = {"PATH": os.defpath, "PIP_CONFIG_FILE": os.devnull}
    python = output / "venv" / "bin" / "python"
    subprocess.run([sys.executable, "-I", "-m", "venv", "--without-pip", str(output / "venv")],
                   env=env, check=True, timeout=limits["venv_seconds"])
    installer = output / "wheels" / wheels["pip"]["path"]
    flags = ["--no-index", "--no-deps", "--require-hashes", "--only-binary=:all:",
             "--disable-pip-version-check", "--no-cache-dir", "--no-compile",
             "--find-links", (output / "wheels").as_uri()]
    (output / "installer.lock").write_text(
        f"pip=={wheels['pip']['version']} --hash=sha256:{wheels['pip']['sha256']}\n", encoding="ascii")
    subprocess.run([str(python), "-I", "-c", PIP_FROM_WHEEL, str(installer), "install",
                    *flags, "-r", str(output / "installer.lock")],
                   env=env, check=True, timeout=limits["dependency_seconds"])
    subprocess.run([str(python), "-I", "-m", "pip", "install", *flags, "-r", str(output / "tokenizer.lock")],
                   env=env, check=True, timeout=limits["dependency_seconds"])
    pip = [str(python), "-I", "-m", "pip"]
    subprocess.run([*pip, "check", "--disable-pip-version-check"],
                   env=env, check=True, timeout=limits["import_seconds"])
    packages = subprocess.run([*pip, "list", "--format", "json", "--disable-pip-version-check"],
                              env=env, check=True, timeout=limits["import_seconds"], capture_output=True).stdout
    installed = {normalized(item["name"]): item["version"] for item in json.loads(packages)}
    require(installed == {name: wheel["version"] for name, wheel in wheels.items()},
            "Installed package set differs from the staged wheelhouse")


def stage(repo, source, model, output):
    require(sys.platform == "linux" and platform.machine() == "x86_64"
            and sys.implementation.name == "cpython", "Expected Linux amd64 CPython")
    training = repo / "runtimes" / "training"
    runtime_bytes = read_contract(training / "runtime.json")
    runtime = json.loads(runtime_bytes)
    require(runtime["schema_version"] == 1, "Unknown training runtime manifest")
    require(tuple(map(int, runtime["python"].split(".")[:2])) == sys.version_info[:2],
            "Python major/minor differs from the tokenizer runtime lock")
    libc, libc_version = platform.libc_ver()
    require(libc == "glibc" and tuple(map(int, libc_version.split("."))) >=
            tuple(map(int, runtime["linux_target"]["minimum_glibc"].split("."))), "Unsupported glibc")
    lock_bytes = read_contract(training / "requirements" / LOCK)
    pins, requirements = project_lock(lock_bytes, runtime["locks"][LOCK])
    catalogue_bytes = read_contract(repo / "api" / "agent" / "v1" / "model-catalog.json")
    catalogue = json.loads(catalogue_bytes)
    require(catalogue["model_key"] == "qwen3-0.6b" and catalogue["license"] == "Apache-2.0",
            "Unexpected tokenizer catalogue identity or license")
    assets = {entry["path"]: entry for entry in catalogue["files"]}
    require(MODEL_FILES <= assets.keys(), "Missing tokenizer catalogue assets")

    output.mkdir(mode=0o700)
    try:
        (output / "tokenizer.lock").write_bytes(requirements)
        wheels = wheelhouse(source, output / "wheels", runtime, pins)
        (output / "model").mkdir(mode=0o700)
        model_files = [
            copy_checked(model / name, output / "model" / name, {assets[name]["sha256"]}, assets[name]["size_bytes"])
            for name in sorted(MODEL_FILES)
        ]
        install(output, runtime, wheels)
        manifest = {
            "schema_version": 1, "profile": "tokenizer-fixture", "target": "linux-amd64",
            "python": platform.python_version(), "glibc": libc_version,
            "runtime_sha256": hashlib.sha256(runtime_bytes).hexdigest(),
            "source_lock_sha256": runtime["locks"][LOCK],
            "projected_lock_sha256": hashlib.sha256(requirements).hexdigest(),
            "wheels": [wheels[name] for name in sorted(wheels)],
            "model": {"repository": catalogue["repository"], "revision": catalogue["revision"],
                      "license": catalogue["license"], "catalogue_sha256": hashlib.sha256(catalogue_bytes).hexdigest(),
                      "files": model_files},
        }
        pending = output / "manifest.tmp"
        pending.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8")
        pending.rename(output / "manifest.json")
    except BaseException:
        shutil.rmtree(output)
        raise


def main():
    """Verify explicit local inputs and install into a fresh private directory."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--wheelhouse", required=True, type=Path)
    parser.add_argument("--model", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    try:
        stage(Path(__file__).resolve().parents[3], args.wheelhouse, args.model, args.output.absolute())
    except (OSError, KeyError, ValueError, subprocess.SubprocessError, zipfile.BadZipFile) as error:
        parser.exit(1, f"Tokenizer fixture: {error}\n")


if __name__ == "__main__":
    main()
