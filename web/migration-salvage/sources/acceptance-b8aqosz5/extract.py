from pathlib import Path
import base64
import json
import shutil
import sys

run, output = map(Path, sys.argv[1:])
output.mkdir(parents=True,exist_ok=True)
scope_names = {
    "home-foreign-workspace-refused": "s301-refused.png",
    "models-foreign-workspace-refused": "s304-refused.png",
    "models-selected-workspace": "s304-selected.png",
    "home-selected-workspace-after-history": "s301-history.png",
    "home-second-person": "s301-session.png",
    "home-second-person-phone": "s301-session-phone.png",
}
def attachments(value):
    if isinstance(value,list):
        for child in value:
            yield from attachments(child)
    elif isinstance(value,dict):
        if value.get("contentType")=="image/png":
            yield value
        for child in value.values():
            yield from attachments(child)

for report, prefix in [
    ("playwright-report.json",""),
    ("console-help-report.json","console-"),
    ("portal-help-report.json","portal-"),
]:
    path=run/report
    if not path.is_file():
        continue
    for item in attachments(json.loads(path.read_text())):
        name=item["name"]
        if prefix:
            if not name.startswith(("workspace-parameter-","password-parameters-","apply-cutover-errors-")):
                continue
            filename=prefix+name+".png"
        else:
            filename=scope_names.get(name)
            if not filename:
                continue
        if "body" in item:
            (output/filename).write_bytes(base64.b64decode(item["body"]))
        else:
            shutil.copyfile(item["path"],output/filename)
if (run/"scope-proof.json").exists():
    shutil.copyfile(run/"scope-proof.json",output/"scope-proof.json")
if (run/"postgres.log").exists():
    proof=[line for line in (run/"postgres.log").read_text().splitlines() if line.startswith("PORTAL_SCOPE_DB_PROOF")]
    (output/"database-proof.log").write_text("\n".join(proof)+"\n")
print("\n".join(sorted(p.name for p in output.iterdir())))
