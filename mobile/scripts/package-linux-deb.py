#!/usr/bin/env python3
"""Stage a DEB from the verified Linux bundle, without executing its payload."""
import argparse
import hashlib
import json
import math
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import tarfile
import tempfile


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def copy(source, destination, executable=False):
    destination.parent.mkdir(parents=True, exist_ok=True)
    shutil.copyfile(source, destination)
    destination.chmod(0o755 if executable else 0o644)


def stage(args):
    repo = args.repo.resolve()
    metadata = repo / "mobile/linux/debian"
    if not re.fullmatch(r"[0-9][A-Za-z0-9.+:~\-]*", args.version):
        raise ValueError("Invalid Debian version")
    if not args.maintainer.strip() or any(ord(c) < 32 for c in args.maintainer):
        raise ValueError("Invalid package maintainer")
    if args.stage.exists():
        raise ValueError("Stage must be a new directory")
    args.stage.mkdir(parents=True)
    with tempfile.TemporaryDirectory(prefix="quantway-deb-input-", dir=args.stage.parent) as temporary:
        extracted = Path(temporary)
        root_name = "quantway-linux-" + args.arch
        with tarfile.open(args.archive, "r:gz") as archive:
            seen = set()
            for member in archive.getmembers():
                relative = PurePosixPath(member.name)
                if (relative.is_absolute() or ".." in relative.parts or not relative.parts or
                        relative.parts[0] != root_name or not (member.isdir() or member.isfile()) or
                        member.name in seen):
                    raise ValueError("Unsafe bundle member: " + member.name)
                seen.add(member.name)
            archive.extractall(extracted)
        root = extracted / root_name
        linux = root / "mobile/linux"
        manifest = {}
        for line in (linux / "SHA256SUMS").read_text().splitlines():
            match = re.fullmatch(r"([0-9a-f]{64})  ([^\r\n\\]+)", line)
            if not match or match[2] in manifest:
                raise ValueError("Invalid bundle manifest")
            path = (linux / match[2]).resolve()
            if not path.is_relative_to(root) or not path.is_file() or digest(path) != match[1]:
                raise ValueError("Bundle checksum mismatch: " + match[2])
            manifest[match[2]] = match[1]
        manifest_files = {(linux / name).resolve() for name in manifest}
        if manifest_files != {p.resolve() for p in root.rglob("*") if p.is_file() and p != linux / "SHA256SUMS"}:
            raise ValueError("Bundle has unmanifested files")
        if (linux / "build/ARCH").read_text().strip() != args.arch:
            raise ValueError("Bundle architecture mismatch")
        source_ui = repo / "mobile/linux/ui"
        expected_ui = {p.name: digest(p) for p in source_ui.iterdir()
                       if p.is_file() and p.suffix in (".py", ".css")}
        actual_ui = {p.name: digest(p) for p in (linux / "ui").iterdir() if p.is_file()}
        if actual_ui != expected_ui:
            raise ValueError("Bundle UI differs from current sources; rebuild it")
        ui_hashes = {}
        app = args.stage / "usr/share/quantway/mobile/linux/ui"
        for path in sorted((linux / "ui").iterdir()):
            if path.name.startswith("test_") or path.name == "qa_screens.py":
                continue
            copy(path, app / path.name)
            ui_hashes[path.name] = digest(path)
        binaries = {}
        for name in ("quantway-helper", "quantway-net-helper", "quantway-tinc-helper", "tinc", "tincd"):
            source = linux / "build" / name
            content = source.read_bytes()
            if (content[:6] != b"\x7fELF\x02\x01" or
                    int.from_bytes(content[18:20], "little") != {"arm64": 183, "amd64": 62}[args.arch]):
                raise ValueError("Wrong ELF architecture: " + name)
            copy(source, args.stage / "usr/libexec/quantway" / name, True)
            binaries[name] = digest(source)
        brand = root / "mobile/android/app/src/main/res/drawable/quantway_brand.png"
        if digest(brand) != digest(repo / "mobile/android/app/src/main/res/drawable/quantway_brand.png"):
            raise ValueError("Bundle brand differs from current source")
        copy(brand, args.stage / "usr/share/quantway/mobile/android/app/src/main/res/drawable/quantway_brand.png")
        copy(brand, args.stage / "usr/share/icons/hicolor/512x512/apps/quantway.png")
        copy(metadata / "quantway", args.stage / "usr/bin/quantway", True)
        copy(metadata / "link.quantway.desktop.desktop", args.stage / "usr/share/applications/link.quantway.desktop.desktop")
        doc = args.stage / "usr/share/doc/quantway"
        for name in ("BUILD-INFO.client", "BUILD-INFO.tinc", "COPYING.tinc"):
            copy(linux / "build" / name, doc / name)
        copy(metadata / "README.Debian", doc / "README.Debian")
        copy(metadata / "copyright", doc / "copyright")
        for source in args.sources.rglob("*"):
            if source.relative_to(args.sources).parts[0] == "native-sources":
                continue  # The corresponding source is already in its archive.
            if source.is_symlink():
                raise ValueError("Source/license material must not contain symlinks")
            if source.is_file():
                copy(source, doc / source.relative_to(args.sources))
        build_info = {"package": "quantway", "version": args.version, "architecture": args.arch,
                      "input_bundle_sha256": digest(args.archive), "ui_sha256": ui_hashes,
                      "binaries_sha256": binaries, "native_source_sha256": digest(args.sources / "native-sources.tar.xz")}
        (doc / "BUILD-INFO.package.json").write_text(json.dumps(build_info, indent=2) + "\n")
    replacements = {"@VERSION@": args.version, "@ARCHITECTURE@": args.arch,
                    "@MAINTAINER@": args.maintainer,
                    "@INSTALLED_SIZE@": str(math.ceil(sum(p.stat().st_size for p in args.stage.rglob("*") if p.is_file()) / 1024)),
                    "@MAINTAINER_CHECK@": (metadata / "maintainer-check.py").read_text()}
    control = args.stage / "DEBIAN"
    control.mkdir()
    for template, name in (("control.in", "control"), ("preinst.in", "preinst"), ("prerm.in", "prerm")):
        text = (metadata / template).read_text()
        for token, value in replacements.items():
            text = text.replace(token, value)
        if re.search(r"@[A-Z_]+@", text):
            raise ValueError("Unresolved metadata token in " + template)
        target = control / name
        target.write_text(text)
        target.chmod(0o755 if name in ("preinst", "prerm") else 0o644)
    for directory in args.stage.rglob("*"):
        if directory.is_dir():
            directory.chmod(0o755)
    args.stage.chmod(0o755)
    # dpkg uses these only for verification, never for trusting privileged files.
    lines = [hashlib.md5(p.read_bytes()).hexdigest() + "  " + p.relative_to(args.stage).as_posix()
             for p in sorted(args.stage.rglob("*")) if p.is_file() and control not in p.parents]
    (control / "md5sums").write_text("\n".join(lines) + "\n")
    args.info.write_text(json.dumps(build_info, indent=2) + "\n")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo", type=Path, required=True)
    parser.add_argument("--archive", type=Path, required=True)
    parser.add_argument("--sources", type=Path, required=True)
    parser.add_argument("--stage", type=Path, required=True)
    parser.add_argument("--info", type=Path, required=True)
    parser.add_argument("--arch", choices=("arm64", "amd64"), required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--maintainer", default="Quantway")
    stage(parser.parse_args())


if __name__ == "__main__":
    main()
