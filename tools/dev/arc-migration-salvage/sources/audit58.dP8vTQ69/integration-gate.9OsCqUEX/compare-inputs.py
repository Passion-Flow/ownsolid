import json
import pathlib
import subprocess
import sys
import tarfile

new, archive, old = map(pathlib.Path, sys.argv[1:])
repo = pathlib.Path.cwd()
fields = (
    "GoFiles", "CgoFiles", "TestGoFiles", "XTestGoFiles",
    "EmbedFiles", "TestEmbedFiles", "XTestEmbedFiles",
    "CFiles", "CXXFiles", "HFiles", "SFiles", "SysoFiles",
)
decoder = json.JSONDecoder()

def inputs(group):
    paths = set()
    for listing in ("control-inputs.json", "verifier-inputs.json"):
        data = (group / listing).read_text()
        i = 0
        while i < len(data):
            while i < len(data) and data[i].isspace():
                i += 1
            if i == len(data):
                break
            package, i = decoder.raw_decode(data, i)
            directory = pathlib.Path(package["Dir"])
            if directory.is_relative_to(repo):
                for field in fields:
                    for name in package.get(field, []):
                        item = directory / name
                        if item.is_relative_to(repo):
                            paths.add(item.relative_to(repo))
    return paths

before, after = inputs(old), inputs(new)
manifests = set(
    pathlib.Path(name)
    for name in subprocess.check_output(["git", "ls-files", "-z"]).decode().split("\0")
    if pathlib.Path(name).name in {"go.mod", "go.sum", "go.work", "go.work.sum"}
)
lineage = set(pathlib.Path("services/control/migrations/postgresql").glob("*.sql"))
lineage.add(pathlib.Path("api/release/v1/mvp-postgresql-cpu-v1-lineage.json"))
expected = {f"repo/{item}": item for item in after | manifests | lineage}
changed = []
with tarfile.open(archive, "r|gz") as source:
    for member in source:
        if member.name not in expected:
            continue
        item = expected.pop(member.name)
        tracked = source.extractfile(member)
        if tracked is None or item.read_bytes() != tracked.read():
            changed.append(str(item))
changed.extend(map(str, expected.values()))
print(f"Selected package and embed inputs: {len(before)} archived and {len(after)} current across {len({item.parent for item in after})} repository package directories.")
print(f"Changed input sets: {len(before ^ after)}. Changed files among {len(after | manifests | lineage)} compiled, module, migration and lineage inputs: {len(changed)}.")
for item in sorted(set(map(str, before ^ after)) | set(changed)):
    print(item)
assert before == after and not changed
