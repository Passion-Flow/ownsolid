"""Pack and stage a bounded, lock-bound pnpm 12 registry metadata snapshot."""
from __future__ import annotations

import argparse
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import re
import stat
import tarfile
import uuid

FORMAT = "projectone.offline.pnpm-metadata.v1"
REGISTRY = "https://registry.npmjs.org"
REGISTRY_DIRECTORY = "https%3A+registry.npmjs.org"
KINDS = ("metadata", "metadata-full")
MAX_ENTRY_BYTES = 64 * 1024**2
MAX_INVENTORY_BYTES = 8 * 1024**2
MAX_PACKAGES = 10000
PACKAGE = re.compile(r"(?:@[A-Za-z0-9._-]+/)?[A-Za-z0-9._-]+")


def require(value, code):
    if not value:
        raise ValueError(code)


def digest(path):
    with path.open("rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def identity(info):
    return (info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns,
            info.st_ctime_ns, info.st_uid, info.st_gid, info.st_mode, info.st_nlink)


def regular(path, maximum):
    info = path.lstat()
    require(path.is_absolute() and path.resolve() == path and stat.S_ISREG(info.st_mode)
            and info.st_uid == os.geteuid() and info.st_nlink == 1
            and not info.st_mode & 0o022 and info.st_size <= maximum,
            "FRONTEND_METADATA_EXACT_REGULAR_FILE_REQUIRED")
    return info


def private_directory(path):
    require(path.is_absolute() and path.resolve() == path and path.is_dir()
            and path.stat().st_uid == os.geteuid() and path.stat().st_mode & 0o777 == 0o700,
            "FRONTEND_METADATA_PRIVATE_DIRECTORY_REQUIRED")


def locked_packages(lockfile):
    """Read the closed package-key subset of pinned pnpm lockfile v9 documents."""
    regular(lockfile, MAX_INVENTORY_BYTES)
    documents = re.split(r"(?m)^---\s*$", lockfile.read_text())
    groups = []
    for document in documents:
        if not document.strip():
            continue
        require(re.search(r"(?m)^lockfileVersion: ['\"]?9\.0['\"]?\s*$", document),
                "FRONTEND_METADATA_LOCK_FORMAT_UNSUPPORTED")
        names, keys, inside = set(), set(), False
        for line in document.splitlines():
            if line == "packages:":
                inside = True
                continue
            if inside and line and not line.startswith(" "):
                inside = False
            if inside and line.startswith("  ") and not line.startswith("   ") and line.strip():
                match = re.fullmatch(r"  (?:'([^']+)'|([^ :]+)):", line)
                require(match, "FRONTEND_METADATA_PACKAGE_KEY_UNSUPPORTED")
                key = match[1] or match[2]
                require("@" in key, "FRONTEND_METADATA_PACKAGE_VERSION_REQUIRED")
                name, version = key.rsplit("@", 1)
                require(PACKAGE.fullmatch(name) and version and key not in keys,
                        "FRONTEND_METADATA_PUBLIC_PACKAGE_REQUIRED")
                keys.add(key)
                names.add(name)
        require(names and len(names) <= MAX_PACKAGES, "FRONTEND_METADATA_LOCK_PACKAGES_REQUIRED")
        groups.append(names)
    require(1 <= len(groups) <= 2, "FRONTEND_METADATA_LOCK_DOCUMENTS_UNSUPPORTED")
    return set.union(*groups), groups[-1]


def relative_metadata_name(name):
    path = PurePosixPath(name)
    require(not path.is_absolute() and ".." not in path.parts and str(path) == name
            and len(path.parts) >= 4 and path.parts[0] == "v12"
            and path.parts[1] in KINDS and path.parts[2] == REGISTRY_DIRECTORY
            and name.endswith(".jsonl"), "FRONTEND_METADATA_NAMESPACE_REQUIRED")
    package = str(PurePosixPath(*path.parts[3:]))[:-6]
    require(PACKAGE.fullmatch(package), "FRONTEND_METADATA_PUBLIC_PACKAGE_REQUIRED")
    return package


def collect(cache, lockfile, package_json, maximum):
    private_directory(cache)
    lock_identity = identity(regular(lockfile, MAX_INVENTORY_BYTES))
    package_identity = identity(regular(package_json, MAX_INVENTORY_BYTES))
    manager = json.loads(package_json.read_bytes())["packageManager"]
    require(re.fullmatch(r"pnpm@12\.[0-9]+\.[0-9]+", manager),
            "FRONTEND_METADATA_PINNED_PNPM12_REQUIRED")
    allowed, required = locked_packages(lockfile)
    files, found, total = [], set(), 0
    # Only explicit package metadata is eligible. Machine state, authentication
    # and lockfile-verification result caches cannot enter this namespace.
    for kind in KINDS:
        for name in sorted(allowed):
            relative = f"v12/{kind}/{REGISTRY_DIRECTORY}/{name}.jsonl"
            path = cache / relative
            if not path.exists() and not path.is_symlink():
                continue
            info = regular(path, MAX_ENTRY_BYTES)
            total += info.st_size
            require(total <= maximum, "FRONTEND_METADATA_TOTAL_LIMIT")
            files.append({"path": relative, "package": name, "bytes": info.st_size,
                          "sha256": digest(path), "mtime_ns": info.st_mtime_ns,
                          "identity": identity(info)})
            found.add(name)
    require(required <= found, "FRONTEND_METADATA_LOCK_COVERAGE_INCOMPLETE")
    require(identity(lockfile.lstat()) == lock_identity
            and identity(package_json.lstat()) == package_identity,
            "FRONTEND_METADATA_INPUT_CHANGED")
    return {"format": FORMAT, "package_manager": manager, "registry": REGISTRY,
            "lock_sha256": digest(lockfile), "required_packages": sorted(required),
            "files": files, "source_identities": {str(lockfile): lock_identity,
                                                     str(package_json): package_identity}}


def fsync_directory(path):
    descriptor = os.open(path, os.O_DIRECTORY)
    try:
        os.fsync(descriptor)
    finally:
        os.close(descriptor)


def pack(cache, lockfile, package_json, output, maximum):
    private_directory(output.parent)
    require(not output.exists() and not output.is_symlink(), "FRONTEND_METADATA_OUTPUT_OCCUPIED")
    inventory = collect(cache, lockfile, package_json, maximum)
    public = {k: v for k, v in inventory.items() if k != "source_identities"}
    public["files"] = [{k: v for k, v in row.items() if k != "identity"}
                       for row in inventory["files"]]
    temporary = output.with_name(output.name + ".partial-" + uuid.uuid4().hex)
    # Interrupted output is retained at this exact private name; it is never
    # published or silently overwritten by a later invocation.
    with temporary.open("xb") as raw:
        os.fchmod(raw.fileno(), 0o600)
        with tarfile.open(fileobj=raw, mode="w:gz") as archive:
            data = (json.dumps(public, sort_keys=True, separators=(",", ":")) + "\n").encode()
            item = tarfile.TarInfo("inventory.json")
            item.mode, item.size = 0o600, len(data)
            archive.addfile(item, io.BytesIO(data))
            for row in inventory["files"]:
                path = cache / row["path"]
                require(identity(regular(path, MAX_ENTRY_BYTES)) == tuple(row["identity"]),
                        "FRONTEND_METADATA_INPUT_CHANGED")
                descriptor = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
                with os.fdopen(descriptor, "rb") as source:
                    require(identity(os.fstat(source.fileno())) == tuple(row["identity"]),
                            "FRONTEND_METADATA_INPUT_CHANGED")
                    item = tarfile.TarInfo("pnpm-cache/" + row["path"])
                    item.mode, item.size = 0o600, row["bytes"]
                    archive.addfile(item, source)
                    require(identity(os.fstat(source.fileno())) == tuple(row["identity"]),
                            "FRONTEND_METADATA_INPUT_CHANGED")
                require(digest(path) == row["sha256"]
                        and identity(path.lstat()) == tuple(row["identity"]),
                        "FRONTEND_METADATA_INPUT_CHANGED")
        raw.flush()
        os.fsync(raw.fileno())
    require(all(identity(Path(name).lstat()) == saved
                for name, saved in inventory["source_identities"].items())
            and digest(lockfile) == inventory["lock_sha256"], "FRONTEND_METADATA_INPUT_CHANGED")
    os.link(temporary, output)  # Exclusive publication, never replacement.
    temporary.unlink()
    fsync_directory(output.parent)
    return {"status": "packed_lock_bound_registry_metadata_offline_verification_pending",
            "archive": str(output), "sha256": digest(output), "files": len(inventory["files"]),
            "metadata_bytes": sum(row["bytes"] for row in inventory["files"]),
            "lock_sha256": inventory["lock_sha256"]}


def extract(archive_path, expected, destination, maximum):
    before = regular(archive_path, maximum + MAX_INVENTORY_BYTES)
    require(re.fullmatch(r"[a-f0-9]{64}", expected) and digest(archive_path) == expected,
            "FRONTEND_METADATA_ARCHIVE_SHA_REQUIRED")
    private_directory(destination.parent)
    require(not destination.exists() and not destination.is_symlink(),
            "FRONTEND_METADATA_DESTINATION_OCCUPIED")
    with tarfile.open(archive_path, "r:gz") as archive:
        members, seen, total = [], set(), 0
        for item in archive:
            require(len(members) < MAX_PACKAGES * len(KINDS) + 1 and item.name not in seen
                    and item.type in (tarfile.REGTYPE, tarfile.AREGTYPE) and item.mode == 0o600
                    and 0 <= item.size <= (MAX_INVENTORY_BYTES if item.name == "inventory.json"
                                          else MAX_ENTRY_BYTES),
                    "FRONTEND_METADATA_ARCHIVE_MEMBERS_REQUIRED")
            total += item.size
            require(total <= maximum + MAX_INVENTORY_BYTES, "FRONTEND_METADATA_TOTAL_LIMIT")
            seen.add(item.name)
            members.append(item)
        require(members, "FRONTEND_METADATA_ARCHIVE_MEMBERS_REQUIRED")
        manifest = next((item for item in members if item.name == "inventory.json"), None)
        require(manifest and manifest.size <= MAX_INVENTORY_BYTES,
                "FRONTEND_METADATA_INVENTORY_REQUIRED")
        inventory = json.loads(archive.extractfile(manifest).read())
        require(inventory["format"] == FORMAT and inventory["registry"] == REGISTRY
                and re.fullmatch(r"pnpm@12\.[0-9]+\.[0-9]+", inventory["package_manager"])
                and re.fullmatch(r"[a-f0-9]{64}", inventory["lock_sha256"]),
                "FRONTEND_METADATA_INVENTORY_INVALID")
        rows = inventory["files"]
        require(rows and len({row["path"] for row in rows}) == len(rows)
                and sum(row["bytes"] for row in rows) <= maximum,
                "FRONTEND_METADATA_TOTAL_LIMIT")
        indexed = {item.name: item for item in members}
        require(set(indexed) == {"inventory.json"} | {"pnpm-cache/" + row["path"] for row in rows},
                "FRONTEND_METADATA_EXACT_NAMESPACE_REQUIRED")
        for row in rows:
            require(relative_metadata_name(row["path"]) == row["package"]
                    and type(row["bytes"]) is int and 0 <= row["bytes"] <= MAX_ENTRY_BYTES
                    and re.fullmatch(r"[a-f0-9]{64}", row["sha256"])
                    and type(row["mtime_ns"]) is int and 0 <= row["mtime_ns"] < 2**63
                    and indexed["pnpm-cache/" + row["path"]].size == row["bytes"],
                    "FRONTEND_METADATA_ENTRY_INVALID")
        require(set(inventory["required_packages"]) <= {row["package"] for row in rows},
                "FRONTEND_METADATA_LOCK_COVERAGE_INCOMPLETE")
        destination.mkdir(mode=0o700)
        for row in rows:
            path = destination / row["path"]
            path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
            with archive.extractfile(indexed["pnpm-cache/" + row["path"]]) as source, path.open("xb") as target:
                os.fchmod(target.fileno(), 0o600)
                while block := source.read(1024**2):
                    target.write(block)
                target.flush()
                os.fsync(target.fileno())
            require(digest(path) == row["sha256"], "FRONTEND_METADATA_ENTRY_SHA_REQUIRED")
            os.utime(path, ns=(row["mtime_ns"], row["mtime_ns"]))
        require(identity(archive_path.lstat()) == identity(before) and digest(archive_path) == expected,
                "FRONTEND_METADATA_ARCHIVE_CHANGED")
        fsync_directory(destination)
    return {"status": "staged_exact_registry_metadata_offline_verification_pending",
            "destination": str(destination), "files": len(rows), "lock_sha256": inventory["lock_sha256"]}


def limit(value):
    number = int(value)
    if not 1 <= number <= 512 * 1024**2:
        raise argparse.ArgumentTypeError("maximum bytes must be 1..536870912")
    return number


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--maximum-bytes", type=limit, default=256 * 1024**2,
                        help="Total uncompressed metadata bytes, 1..536870912; default268435456")
    commands = parser.add_subparsers(dest="command", required=True)
    producer = commands.add_parser("pack")
    for flag in ("cache-root", "lockfile", "package-json", "output"):
        producer.add_argument("--" + flag, type=Path, required=True)
    consumer = commands.add_parser("extract")
    consumer.add_argument("--archive", type=Path, required=True)
    consumer.add_argument("--expected-sha256", required=True)
    consumer.add_argument("--destination", type=Path, required=True)
    args = parser.parse_args()
    os.umask(0o077)
    result = (pack(args.cache_root, args.lockfile, args.package_json, args.output, args.maximum_bytes)
              if args.command == "pack" else
              extract(args.archive, args.expected_sha256, args.destination, args.maximum_bytes))
    print(json.dumps(result))


if __name__ == "__main__":
    main()
