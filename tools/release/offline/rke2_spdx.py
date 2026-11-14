"""Assemble release SPDX from the reviewed RKE2 image observations and notices."""

import argparse
import hashlib
import io
import json
import pathlib
import re
import tarfile

RELEASE = "v1.36.6+rke2r1"
NAMESPACE = "urn:ownsolid:spdx:rke2:v1.36.6-rke2r1:linux-amd64:1"
ROOT = "SPDXRef-RKE2"
DOCUMENT = "SPDXRef-DOCUMENT"
UNKNOWN = "NOASSERTION"
ARCHIVES = {
    "core": ("rke2-images-core.linux-amd64.tar.zst", "58c153e94944039fc05b8e23de9e8f489df049519e55dd33e5790bf5ae19a217"),
    "canal": ("rke2-images-canal.linux-amd64.tar.zst", "40a09eb8460e27e79dc13247517445c9d94461ef5078ef9e4d7fe1f95aced8b0"),
    "binary": ("rke2.linux-amd64.tar.gz", "e111ac6333ddb2b819d133a413914d4456dd2195a3d694865cb7acda5bf81ae4"),
}


def encode(value):
    return (json.dumps(value, ensure_ascii=True, indent=2, sort_keys=True) + "\n").encode()


def digest(raw):
    return hashlib.sha256(raw).hexdigest()


def spdx_id(kind, identity):
    return f"SPDXRef-{kind}-{digest(encode(identity))}"


def checksums(sha256):
    return [{"algorithm": "SHA256", "checksumValue": sha256.removeprefix("sha256:")}]


def package(identifier, name, purpose, **fields):
    return {
        "SPDXID": identifier, "name": name, "primaryPackagePurpose": purpose,
        "downloadLocation": UNKNOWN, "filesAnalyzed": False,
        "licenseDeclared": UNKNOWN, "licenseConcluded": UNKNOWN,
        "copyrightText": UNKNOWN, **fields,
    }


def relationship(source, kind, target, comment=""):
    result = {"spdxElementId": source, "relationshipType": kind, "relatedSpdxElement": target}
    if comment:
        result["comment"] = comment
    return result


def read_by_digest(directory, sha256):
    if not re.fullmatch("[0-9a-f]{64}", sha256):
        raise ValueError("invalid evidence digest")
    raw = (directory / sha256).read_bytes()
    if digest(raw) != sha256:
        raise ValueError("evidence bytes differ from the observation")
    return raw


def declared_license(component):
    values = sorted({license["spdxExpression"] for license in component["licenses"]
                     if license["spdxExpression"] and "LicenseRef-" not in license["spdxExpression"]})
    if len(values) == 1:
        return values[0]
    return UNKNOWN


def component_package(component):
    identity = [component[key] for key in ("type", "name", "version", "purl")]
    license = declared_license(component)
    metadata = component.get("metadata") or {}
    facts = {key: metadata[key] for key in ("h1Digest", "sourceRpm", "originPackage", "gitCommitOfApkPort", "architecture")
             if metadata.get(key)}
    raw_licenses = sorted({entry["value"] for entry in component["licenses"] if entry["value"]})
    identifier = spdx_id("Component", [identity, license, raw_licenses, facts])
    purpose = {"go-module": "LIBRARY", "binary": "APPLICATION"}.get(component["type"], "OTHER")
    node = package(identifier, component["name"], purpose, versionInfo=component["version"], licenseDeclared=license)
    if component["purl"]:
        node["externalRefs"] = [{"referenceCategory": "PACKAGE-MANAGER", "referenceType": "purl",
                                 "referenceLocator": component["purl"]}]
    if facts:
        node["sourceInfo"] = ", ".join(f"{key}={value}" for key, value in sorted(facts.items()))
    if raw_licenses:
        node["licenseComments"] = "Observed declarations: " + ", ".join(raw_licenses)
    return node


def assemble(inputs, source_dir, out):
    packages = {ROOT: package(ROOT, "RKE2 Linux amd64 delivery inputs", "OPERATING_SYSTEM", versionInfo=RELEASE)}
    files, archive_files = {}, {}
    edges = [relationship(DOCUMENT, "DESCRIBES", ROOT)]
    sources = json.loads((source_dir / "source-index.json").read_bytes())
    go_sources = {(entry["module"], entry["version"], entry["h1"]): entry
                  for entry in json.loads((source_dir / "module-source-index.json").read_bytes())}
    apk_sources = {(entry["origin"], entry["commit"]): entry
                   for entry in json.loads((source_dir / "apk-source-index.json").read_bytes())}
    linked_sources = set()
    by_image = {}
    for source in sources:
        by_image.setdefault(source["image"], []).append(source)
    for group, (name, checksum) in ARCHIVES.items():
        identifier = "SPDXRef-Archive-" + group
        packages[identifier] = package(identifier, name, "ARCHIVE", checksums=checksums(checksum))
        edges.append(relationship(ROOT, "CONTAINS", identifier))

    def evidence(name, raw, documented, comment):
        if name not in archive_files:
            archive_files[name] = raw
        elif archive_files[name] != raw:
            raise ValueError("conflicting evidence paths")
        identifier = spdx_id("Evidence", name)
        files[identifier] = {
            "SPDXID": identifier, "fileName": "./" + name,
            "checksums": [{"algorithm": "SHA1", "checksumValue": hashlib.sha1(raw, usedforsecurity=False).hexdigest()},
                          *checksums(digest(raw))],
            "licenseConcluded": UNKNOWN, "copyrightText": UNKNOWN,
        }
        edges.append(relationship(identifier, "DOCUMENTATION_OF", documented, comment))
        return identifier

    observations = sorted(inputs.glob("*.json"))
    group_counts = {"core": 0, "canal": 0}
    for observation in observations:
        image = json.loads(observation.read_bytes())
        group_counts[image["group"]] += 1
        slug = observation.stem
        identifier = spdx_id("Image", image["reference"])
        node = package(identifier, image["reference"], "CONTAINER", checksums=checksums(image["manifest"]),
                       versionInfo=image["reference"].rsplit(":", 1)[1])
        node["sourceInfo"] = "Linux amd64 OCI image manifest. Config and ordered layers are represented below."
        for key, value in sorted(image["labels"].items()):
            if key.endswith(".source"):
                node["sourceInfo"] += f" {key}: {value}."
        packages[identifier] = node
        edges.append(relationship("SPDXRef-Archive-" + image["group"], "CONTAINS", identifier))
        for position, sha256 in enumerate([image["config"], *image["layers"]]):
            blob = spdx_id("Blob", sha256)
            packages[blob] = package(blob, sha256, "FILE", checksums=checksums(sha256))
            role = "OCI config" if position == 0 else f"OCI layer {position}"
            edges.append(relationship(identifier, "CONTAINS", blob, role))
        components, locations = {}, {}
        for component in image["components"]:
            entry = component_package(component)
            key = entry["SPDXID"]
            packages[key] = entry
            components.setdefault(component["name"], set()).add(key)
            locations.setdefault(key, set()).update(location["path"] for location in component["locations"])
            if key in linked_sources:
                continue
            linked_sources.add(key)
            metadata = component.get("metadata") or {}
            module = go_sources.get((component["name"], component["version"], metadata.get("h1Digest")))
            if module:
                for notice in module["notices"]:
                    checksum = notice["sha256"]
                    evidence("licenses/" + checksum, read_by_digest(source_dir / "module-sources", checksum), key,
                             f"{notice['file']} from {module['source']}, matching module {module['h1']}")
            recipe = apk_sources.get((metadata.get("originPackage"), metadata.get("gitCommitOfApkPort")))
            if recipe:
                checksum = recipe["digest"]
                evidence("source-recipes/" + checksum, read_by_digest(source_dir / "apk-sources", checksum),
                         key, "Alpine source recipe at " + recipe["source"])
        for child, paths in sorted(locations.items()):
            edges.append(relationship(identifier, "CONTAINS", child, "Observed at " + ", ".join(sorted(paths))))
        for path, sha256 in sorted(image["notices"].items()):
            raw = read_by_digest(inputs / "licenses", sha256)
            name = "licenses/" + sha256
            notice = evidence(name, raw, identifier, "Embedded notice at /" + path)
            parts = pathlib.PurePosixPath(path).parts
            if len(parts) >= 5 and parts[:3] in (("usr", "share", "licenses"), ("usr", "share", "doc")):
                for component_id in sorted(components.get(parts[3], ())):
                    edges.append(relationship(notice, "DOCUMENTATION_OF",
                                              component_id, "Embedded package notice at /" + path))
        for source in sorted(by_image.get(slug, []), key=lambda item: item["source"]):
            raw = read_by_digest(source_dir / "source", source["digest"])
            name = "licenses/" + source["digest"]
            notice = evidence(name, raw, identifier, f"Upstream {source['license']} source notice at {source['source']}")
            for child in sorted(components.get(source["module"], ())):
                edges.append(relationship(notice, "DOCUMENTATION_OF", child,
                                          "Licence of the cited upstream source"))
        for statement in sorted((inputs / "upstream").glob(slug + "-*.json")):
            raw = statement.read_bytes()
            data = json.loads(raw)
            if not any(subject.get("digest", {}).get("sha256") == image["manifest"].removeprefix("sha256:")
                       for subject in data["subject"]):
                raise ValueError("upstream evidence does not name the observed image")
            evidence("upstream/" + statement.name, raw, identifier, data["predicateType"])
    if group_counts != {"core": 14, "canal": 2}:
        raise ValueError("the approved image sets are incomplete")

    binary = (source_dir / "rke2-binary-license.txt").read_bytes()
    evidence("licenses/" + digest(binary), binary, "SPDXRef-Archive-binary", "Embedded share/rke2/LICENSE.txt")
    notice_path = out / "component-notices.tar"
    with tarfile.open(notice_path, "x", format=tarfile.USTAR_FORMAT) as archive:
        for name, raw in sorted(archive_files.items()):
            member = tarfile.TarInfo(name)
            member.mode, member.size = 0o644, len(raw)
            archive.addfile(member, io.BytesIO(raw))
    with notice_path.open("rb") as archive:
        notice_digest = hashlib.file_digest(archive, "sha256").hexdigest()
    notice_id = "SPDXRef-RKE2-NoticeArchive"
    packages[notice_id] = package(notice_id, notice_path.name, "ARCHIVE", checksums=checksums(notice_digest))
    edges.append(relationship(ROOT, "CONTAINS", notice_id))
    edges.extend(relationship(notice_id, "CONTAINS", identifier) for identifier in sorted(files))
    document = {
        "spdxVersion": "SPDX-2.3", "dataLicense": "CC0-1.0", "SPDXID": DOCUMENT,
        "name": f"ownsolid RKE2 {RELEASE} Linux amd64 component evidence",
        "documentNamespace": NAMESPACE,
        "creationInfo": {"created": "2026-11-09T00:00:00Z", "creators": ["Tool: Syft-1.55.0", "Tool: ownsolid-rke2-spdx-1"]},
        "packages": [packages[key] for key in sorted(packages)], "files": [files[key] for key in sorted(files)],
        "relationships": sorted(edges, key=lambda edge: tuple(edge.get(key, "") for key in
                                                               ("spdxElementId", "relationshipType", "relatedSpdxElement", "comment"))),
    }
    path = out / "components.spdx.json"
    with path.open("xb") as output:
        output.write(encode(document))
    print(f"{len(packages)} packages, {len(files)} evidence files, {len(edges)} relationships")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("observations", type=pathlib.Path)
    parser.add_argument("source", type=pathlib.Path)
    parser.add_argument("output", type=pathlib.Path)
    args = parser.parse_args()
    assemble(args.observations, args.source, args.output)
