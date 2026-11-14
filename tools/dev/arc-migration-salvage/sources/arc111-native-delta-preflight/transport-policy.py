import gzip
import hashlib
import io
import json
import lzma
import os
from pathlib import Path, PurePosixPath
import stat
import tarfile
import time


def send_payload(write_piece, wait_writable, now, deadline, data, receipt):
    """Count only bytes accepted by the local OS pipe, not remote receipt."""
    started = now()
    written = 0
    digest = hashlib.sha256()
    try:
        while written < len(data):
            remaining = deadline - now()
            if remaining <= 0:
                raise TimeoutError("LOCAL_PAYLOAD_WRITE_DEADLINE_EXCEEDED")
            if not wait_writable(min(remaining, 0.25)):
                continue
            try:
                amount = write_piece(memoryview(data)[written:written + 16384])
            except BlockingIOError:
                continue
            if not 0 < amount <= min(16384, len(data) - written):
                raise ValueError("LOCAL_PAYLOAD_WRITE_COUNT_INVALID")
            digest.update(data[written:written + amount])
            written += amount
            receipt.setdefault("first_write_after_seconds", now() - started)
            receipt["last_write_after_seconds"] = now() - started
        return written
    except BaseException as error:
        receipt["error_type"] = type(error).__name__
        receipt["error"] = str(error)[:240]
        raise
    finally:
        receipt.update(expected_bytes=len(data), written_bytes=written,
                       written_sha256=digest.hexdigest(),
                       elapsed_seconds=now() - started,
                       proof_scope="local OS-pipe acceptance; not remote delivery")


def decode_envelope(wire, transport):
    assert transport["encoding"] == "xz-of-tar-v1"
    decoder = lzma.LZMADecompressor(format=lzma.FORMAT_XZ, memlimit=64 * 1024**2)
    raw = decoder.decompress(wire, max_length=transport["decoded_bytes"] + 1)
    assert decoder.eof and not decoder.unused_data, "COMPRESSED_FRAME_BOUNDARY_MISMATCH"
    assert len(raw) == transport["decoded_bytes"], "DECODED_BYTE_COUNT_MISMATCH"
    assert hashlib.sha256(raw).hexdigest() == transport["decoded_sha256"], "DECODED_DIGEST_MISMATCH"
    return raw


def checked_relative(name):
    relative = PurePosixPath(name)
    assert name and str(relative) == name and not relative.is_absolute()
    assert all(part not in {"", ".", ".."} for part in relative.parts)
    return relative


def unpack_members(raw, destination, members):
    actual = set()
    with tarfile.open(fileobj=io.BytesIO(raw), mode="r:") as archive:
        for member in archive:
            checked_relative(member.name)
            assert member.isfile() and member.name in members and member.name not in actual
            expected = members[member.name]
            assert member.size == expected["bytes"]
            data = archive.extractfile(member).read(expected["bytes"] + 1)
            assert len(data) == expected["bytes"] and hashlib.sha256(data).hexdigest() == expected["sha256"]
            target = destination / member.name
            target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
            with target.open("xb") as stream:
                stream.write(data)
            actual.add(member.name)
    assert actual == set(members)


def verify_file(data, reference, mode):
    assert mode == int(reference["mode"], 8) & 0o777, "SOURCE_MODE_MISMATCH"
    assert len(data) == reference["bytes"], "SOURCE_LENGTH_MISMATCH"
    assert hashlib.sha256(data).hexdigest() == reference["sha256"], "SOURCE_DIGEST_MISMATCH"
    blob = b"blob " + str(len(data)).encode() + b"\0" + data
    assert hashlib.sha1(blob).hexdigest() == reference["git_blob"], "SOURCE_GIT_BLOB_MISMATCH"


def read_cached_file(base, name, reference, owner_uid):
    """No symlink traversal or mutable/shared inode is accepted as a source input."""
    relative = checked_relative(name)
    flags = os.O_RDONLY | os.O_NOFOLLOW | os.O_CLOEXEC
    descriptors = []
    try:
        parent = os.open(base, flags | os.O_DIRECTORY)
        descriptors.append(parent)
        assert os.fstat(parent).st_uid == owner_uid, "CACHE_DIRECTORY_OWNER_MISMATCH"
        for part in relative.parts[:-1]:
            parent = os.open(part, flags | os.O_DIRECTORY, dir_fd=parent)
            descriptors.append(parent)
            assert os.fstat(parent).st_uid == owner_uid, "CACHE_DIRECTORY_OWNER_MISMATCH"
        fd = os.open(relative.name, flags, dir_fd=parent)
        descriptors.append(fd)
        before = os.fstat(fd)
        assert stat.S_ISREG(before.st_mode) and before.st_uid == owner_uid and before.st_nlink == 1, "CACHE_FILE_IDENTITY_MISMATCH"
        assert before.st_size == reference["bytes"], "CACHE_FILE_LENGTH_MISMATCH"
        chunks = []
        remaining = reference["bytes"] + 1
        while remaining:
            chunk = os.read(fd, min(remaining, 65536))
            if not chunk:
                break
            chunks.append(chunk)
            remaining -= len(chunk)
        after = os.fstat(fd)
        fields = lambda s: (s.st_dev, s.st_ino, s.st_uid, s.st_nlink, s.st_size, s.st_mode, s.st_mtime_ns, s.st_ctime_ns)
        assert fields(before) == fields(after), "CACHE_FILE_CHANGED_DURING_READ"
        data = b"".join(chunks)
        verify_file(data, reference, stat.S_IMODE(before.st_mode))
        return data
    finally:
        for fd in reversed(descriptors):
            os.close(fd)


def reconstruct_source(root, manifest, transport_binding, base, owner_uid, deadline, apply_patch, now=time.monotonic):
    expected = {item["path"]: item for item in manifest["files"]}
    assert len(expected) == len(manifest["files"]) == 5102
    changed = set(transport_binding["delta_paths"])
    assert len(changed) == transport_binding["delta_path_count"] == 240
    assert changed <= expected.keys()
    patch = root / "source-delta.patch"
    assert patch.stat().st_size == transport_binding["delta_patch_bytes"]
    assert hashlib.sha256(patch.read_bytes()).hexdigest() == transport_binding["delta_patch_sha256"]
    basis = {name: ref for name, ref in expected.items() if name not in changed}
    for ref in transport_binding["basis_changed_or_removed_inputs"]:
        assert ref["path"] not in basis
        basis[ref["path"]] = ref
    assert len(basis) == 5015
    destination = root / "source"
    destination.mkdir(mode=0o700)
    for name, reference in sorted(basis.items()):
        assert now() < deadline, "SOURCE_RECONSTRUCTION_ADMISSION_DEADLINE_EXCEEDED"
        checked_relative(name)
        assert reference["mode"] in {"100644", "100755"}
        data = read_cached_file(base, name, reference, owner_uid)
        target = destination / name
        target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        with target.open("xb") as stream:
            stream.write(data)
        os.chmod(target, int(reference["mode"], 8) & 0o777)
    assert now() < deadline, "SOURCE_RECONSTRUCTION_ADMISSION_DEADLINE_EXCEEDED"
    apply_patch(destination, patch)
    total = 0
    for name, reference in sorted(expected.items()):
        assert now() < deadline, "SOURCE_RECONSTRUCTION_ADMISSION_DEADLINE_EXCEEDED"
        checked_relative(name)
        target = destination / name
        identity = target.lstat()
        assert stat.S_ISREG(identity.st_mode) and identity.st_uid == owner_uid and identity.st_nlink == 1
        verify_file(target.read_bytes(), reference, stat.S_IMODE(target.lstat().st_mode))
        total += reference["bytes"]
    assert {p.relative_to(destination).as_posix() for p in destination.rglob("*") if p.is_file()} == expected.keys()
    assert not any(p.is_symlink() for p in destination.rglob("*"))
    return {"target_paths": len(expected), "delta_paths": len(changed), "cached_paths": len(expected) - len(changed),
            "basis_file_inputs_verified": len(basis),
            "target_bytes": total, "every_byte_mode_and_git_blob_verified": True,
            "cache_read_only": True, "original_source_archive_transferred": False}
