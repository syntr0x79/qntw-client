#!/usr/bin/env python3
"""Collect pinned, patched tinc source and cached Linux Go dependency notices.

No application license is inferred. Git fetches use only the pinned public tinc
URL; Go dependency discovery is offline and never downloads missing modules.
"""
from __future__ import annotations

import argparse
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import re
import shlex
import shutil
import subprocess
import tarfile
import tempfile


PUBLIC_UPSTREAM = "https://github.com/gsliepen/tinc.git"
COMMANDS = ("./linux/cmd/quantway", "./linux/cmd/quantway-net-helper", "./linux/cmd/quantway-tinc-helper")
LICENSE = re.compile(r"^(?:LICEN[CS]E|COPYING|NOTICE)(?:[._-].*)?$", re.I)


def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def write_json(path, value):
    path.write_text(json.dumps(value, ensure_ascii=False, sort_keys=True, indent=2) + "\n")


def run(argv, *, cwd=None, env=None):
    result = subprocess.run(argv, cwd=cwd, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if result.returncode:
        raise RuntimeError(f"{Path(argv[0]).name} failed ({result.returncode}): " +
                           result.stderr.decode("utf-8", "replace")[-2000:].strip())
    return result.stdout


def pins(path):
    values = {}
    for line in path.read_text().splitlines():
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        key, separator, value = line.partition("=")
        parsed = shlex.split(value, comments=True)
        if not separator or not re.fullmatch(r"TINC_[A-Z_]+", key) or len(parsed) != 1:
            raise RuntimeError("unsupported source-pin.env entry")
        values[key] = parsed[0]
    if (values.get("TINC_UPSTREAM_URL") != PUBLIC_UPSTREAM or
            values.get("TINC_UPSTREAM_TAG") != "release-1.1pre18" or
            not re.fullmatch(r"[0-9a-f]{40}", values.get("TINC_UPSTREAM_COMMIT", "")) or
            not re.fullmatch(r"[A-Za-z0-9.+_-]+", values.get("TINC_CLIENT_VERSION", ""))):
        raise RuntimeError("expected a public tinc release-1.1pre18 pin and fixed commit/version")
    return values


def git_environment():
    env = dict(os.environ)
    for key in list(env):
        if key.startswith("GIT_") or key in ("SSH_AUTH_SOCK", "SSH_ASKPASS"):
            del env[key]
    env.update(GIT_CONFIG_NOSYSTEM="1", GIT_CONFIG_GLOBAL=os.devnull,
               GIT_TERMINAL_PROMPT="0", GIT_ASKPASS="false")
    return env


def git(repository, *args, env):
    return run(["git", "-c", "credential.helper=", "-c", "core.hooksPath=" + os.devnull,
                "-c", "protocol.file.allow=never", "-c", "protocol.ssh.allow=never",
                "-c", "protocol.ext.allow=never", "-C", str(repository), *args], env=env)


def upstream(cache, supplied, pin, env):
    repository = supplied.resolve() if supplied else cache / "upstream.git"
    if not repository.exists():
        if supplied:
            raise RuntimeError("supplied upstream repository does not exist")
        run(["git", "init", "--bare", str(repository)], env=env)
    commit = pin["TINC_UPSTREAM_COMMIT"]
    try:
        actual = git(repository, "rev-parse", "--verify", commit + "^{commit}", env=env).decode().strip()
    except RuntimeError:
        if supplied:
            raise RuntimeError("supplied repository does not contain the pinned commit") from None
        git(repository, "fetch", "--no-tags", "--depth=1", PUBLIC_UPSTREAM,
            pin["TINC_UPSTREAM_TAG"], env=env)
        actual = git(repository, "rev-parse", "FETCH_HEAD^{commit}", env=env).decode().strip()
    if actual != commit:
        raise RuntimeError("public release commit does not match source-pin.env")
    return repository


def extract_archive(data, destination):
    # The pinned Git tree contains regular files/directories only. Fail rather
    # than following a future unexpected link outside the build context.
    executable_paths = []
    with tarfile.open(fileobj=io.BytesIO(data)) as archive:
        for member in archive:
            name = PurePosixPath(member.name)
            if name.is_absolute() or ".." in name.parts or not (member.isfile() or member.isdir()):
                raise RuntimeError("unexpected path/link in pinned source archive")
            path = destination.joinpath(*name.parts)
            if member.isdir():
                path.mkdir(parents=True, exist_ok=True)
            else:
                if member.mode & 0o111:
                    executable_paths.append(str(name))
                path.parent.mkdir(parents=True, exist_ok=True)
                with archive.extractfile(member) as source:
                    path.write_bytes(source.read())
                path.chmod(0o755 if member.mode & 0o111 else 0o644)
    return sorted(executable_paths)


def native_sources(repo, stage, repository, pin, env):
    native = repo / "mobile/native/tinc"
    root = stage / "native-sources"
    source = root / "source"
    source.mkdir(parents=True)
    commit = pin["TINC_UPSTREAM_COMMIT"]
    executable_paths = extract_archive(git(repository, "archive", "--format=tar", commit, env=env), source)
    for name in ("socket_guard.c", "socket_guard.h"):
        shutil.copyfile(native / name, source / "src" / name)
    groups = ("deploy/tinc/patches", "mobile/native/tinc/patches", "deploy/tinc-client/patches")
    applied = []
    apply_env = dict(env, GIT_CEILING_DIRECTORIES=str(source.parent))
    for group in groups:
        patches = sorted((repo / group).glob("*.patch"))
        if not patches:
            raise RuntimeError("missing native patch group: " + group)
        for patch in patches:
            git(source, "apply", "--check", str(patch), env=apply_env)
            git(source, "apply", str(patch), env=apply_env)
            target = root / "patches" / patch.relative_to(repo)
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(patch, target)
            applied.append({"path": str(patch.relative_to(repo)), "sha256": sha(patch)})
    configure = source / "configure.ac"
    text = configure.read_text()
    old = "AC_INIT([tinc], m4_esyscmd_s((git describe || echo UNKNOWN) | sed 's/release-//'))"
    if text.count(old) != 1:
        raise RuntimeError("upstream AC_INIT changed; compare build-tinc-linux.sh")
    configure.write_text(text.replace(old, "AC_INIT([tinc], [" + pin["TINC_CLIENT_VERSION"] + "])", 1))
    metadata = git(repository, "show", "-s", "--format=%H%n%T%n%ct%n%cI", commit, env=env).decode().splitlines()
    epoch = int(metadata[2])
    for name in ("Dockerfile.linux", "source-pin.env", "socket_guard.c", "socket_guard.h"):
        shutil.copyfile(native / name, root / name)
    shutil.copyfile(repo / "mobile/scripts/build-tinc-linux.sh", root / "build-tinc-linux.sh.reference")
    (root / "SOURCE_DATE_EPOCH").write_text(str(epoch) + "\n")
    info = {"upstream_url": PUBLIC_UPSTREAM, "upstream_tag": pin["TINC_UPSTREAM_TAG"],
            "commit": metadata[0], "tree": metadata[1], "commit_time": metadata[3],
            "source_date_epoch": epoch, "client_version": pin["TINC_CLIENT_VERSION"],
            "patch_order": applied, "configure_version_fixed": True,
            "upstream_executable_paths": executable_paths,
            "source_pin_sha256": sha(native / "source-pin.env"),
            "socket_guard_sha256": {name: sha(native / name) for name in ("socket_guard.c", "socket_guard.h")},
            "dockerfile_sha256": sha(root / "Dockerfile.linux")}
    write_json(root / "SOURCE.json", info)
    (root / "README.txt").write_text(
        "Quantway Linux tinc corresponding source\n\n"
        "source/ is the archived pinned upstream commit with socket_guard.c/.h copied,\n"
        "all patches applied in SOURCE.json order, and AC_INIT fixed exactly as in\n"
        "mobile/scripts/build-tinc-linux.sh. Do not apply the included patches twice.\n"
        "Upstream licensing is in source/COPYING. No application license is inferred.\n\n"
        "Build tinc/tincd with Docker/buildx (Alpine packages require public network):\n"
        "  docker buildx build --platform linux/arm64 -f Dockerfile.linux \\\n"
        "    --build-arg SOURCE_DATE_EPOCH=$(cat SOURCE_DATE_EPOCH) \\\n"
        "    --target artifacts --output type=local,dest=artifacts-arm64 .\n"
        "Use linux/amd64 and a different destination for AMD64. Dockerfile.linux\n"
        "records configure flags, compiler flags, static linking and build tools.\n"
        "The Alpine tag/package repositories are not a historical package snapshot;\n"
        "this source collection does not claim byte-for-byte future rebuilds.\n")
    with (root / "README.txt").open("a") as readme:
        readme.write("\nRegular files are normalized to 0644, directories and upstream Git\n"
                     "executables to 0755. SOURCE.json lists those executable paths, including\n"
                     "the upstream test scripts; no permission restoration is needed.\n")
    return root, info, epoch


def json_stream(data):
    decoder = json.JSONDecoder()
    text = data.decode()
    offset = 0
    while offset < len(text):
        while offset < len(text) and text[offset].isspace():
            offset += 1
        if offset == len(text):
            break
        value, offset = decoder.raw_decode(text, offset)
        yield value


def go_licenses(repo, stage, cache, go_binary):
    env = dict(os.environ, GOOS="linux", CGO_ENABLED="0", GOWORK="off", GOPROXY="off",
               GOSUMDB="off", GOTOOLCHAIN="local", GOENV="off", GOFLAGS="",
               GOPRIVATE="", GONOPROXY="none", GONOSUMDB="none", GOVCS="*:off",
               GOCACHE=str(cache / "go-build"))
    modules = {}
    application = set()
    for arch in ("arm64", "amd64"):
        env["GOARCH"] = arch
        data = run([go_binary, "list", "-mod=readonly", "-deps", "-json", *COMMANDS], cwd=repo / "mobile", env=env)
        for package in json_stream(data):
            module = package.get("Module")
            if not module:
                continue  # Standard library, not a third-party Go module.
            actual = module.get("Replace", module)
            directory = Path(actual.get("Dir") or module.get("Dir") or "").resolve()
            if module.get("Main") or directory in (repo, repo / "mobile"):
                application.add(module["Path"])
                continue
            if not directory.is_dir():
                raise RuntimeError("dependency source absent from cache: " + module["Path"])
            key = (module["Path"], module.get("Version", ""), actual.get("Path", ""), actual.get("Version", ""))
            item = modules.setdefault(key, {"module": module["Path"], "version": module.get("Version", ""),
                                           "replacement": module.get("Replace", {}).get("Path"),
                                           "replacement_version": module.get("Replace", {}).get("Version"),
                                           "directory": directory, "packages": set(), "architectures": set()})
            item["packages"].add(package["ImportPath"])
            item["architectures"].add(arch)
    destination = stage / "third-party-licenses"
    destination.mkdir()
    index = []
    for key, item in sorted(modules.items()):
        identity = "@".join((item["module"], item["version"]))
        folder = re.sub(r"[^A-Za-z0-9._-]", "_", identity)[:140] + "-" + hashlib.sha256(repr(key).encode()).hexdigest()[:12]
        files = []
        for path in sorted(item.pop("directory").iterdir()):
            if LICENSE.fullmatch(path.name) and path.is_file() and not path.is_symlink():
                target = destination / folder / path.name
                target.parent.mkdir(exist_ok=True)
                shutil.copyfile(path, target)
                files.append({"file": str(target.relative_to(destination)), "sha256": sha(target)})
        item["packages"] = sorted(item["packages"])
        item["architectures"] = sorted(item["architectures"])
        item["license_files"] = files
        index.append(item)
    write_json(destination / "INDEX.json", {"method": "offline go list -deps -json, arm64+amd64 union, top-level LICENSE/COPYING/NOTICE files",
                                            "commands": list(COMMANDS), "modules": index,
                                            "application_modules_excluded": sorted(application),
                                            "modules_without_top_level_notice": [x["module"] for x in index if not x["license_files"]]})
    return {"state": "collected", "modules": len(index), "notice_files": sum(len(x["license_files"]) for x in index)}


def normalize(root, epoch):
    for path in sorted(root.rglob("*")) + [root]:
        if path.is_symlink():
            raise RuntimeError("unexpected symlink in collected output")
        path.chmod(0o755 if path.is_dir() or path.stat().st_mode & 0o111 else 0o644)
        os.utime(path, (epoch, epoch))


def archive_native(root, destination, epoch):
    with tarfile.open(destination, "w:xz", format=tarfile.PAX_FORMAT) as archive:
        for path in [root, *sorted(root.rglob("*"))]:
            info = archive.gettarinfo(str(path), arcname=str(Path(root.name) / path.relative_to(root)))
            info.uid = info.gid = 0
            info.uname = info.gname = "root"
            info.mtime = epoch
            info.pax_headers = {}
            if path.is_file():
                with path.open("rb") as source:
                    archive.addfile(info, source)
            else:
                archive.addfile(info)
    destination.chmod(0o644)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--repo-root", type=Path, default=Path(__file__).resolve().parents[2])
    parser.add_argument("--output", type=Path, required=True, help="new or empty output directory")
    parser.add_argument("--cache", type=Path, default=Path(tempfile.gettempdir()) / "quantway-linux-deb-source-cache")
    parser.add_argument("--upstream-repository", type=Path, help="read-only local Git repository containing the pin; never fetched")
    parser.add_argument("--skip-go-licenses", action="store_true", help="explicit native-only collection")
    parser.add_argument("--go-binary", default="go", help="installed/cached Go toolchain satisfying mobile/go.mod (no automatic download)")
    args = parser.parse_args()
    repo, output, cache = args.repo_root.resolve(), args.output.resolve(), args.cache.resolve()
    if output.exists() and any(output.iterdir()):
        parser.error("--output must be empty; no existing files are overwritten")
    cache.mkdir(parents=True, exist_ok=True, mode=0o700)
    if cache.stat().st_uid != os.geteuid() or cache.stat().st_mode & 0o077:
        parser.error("--cache must be a private directory owned by the current user")
    pin = pins(repo / "mobile/native/tinc/source-pin.env")
    env = git_environment()
    repository = upstream(cache, args.upstream_repository, pin, env)
    with tempfile.TemporaryDirectory(prefix="collection-", dir=cache) as work:
        stage = Path(work)
        native, info, epoch = native_sources(repo, stage, repository, pin, env)
        licenses = {"state": "explicitly_skipped"} if args.skip_go_licenses else go_licenses(repo, stage, cache, args.go_binary)
        normalize(native, epoch)
        archive_native(native, stage / "native-sources.tar.xz", epoch)
        write_json(stage / "COLLECTION.json", {"native": info, "native_archive_sha256": sha(stage / "native-sources.tar.xz"),
                                               "go_licenses": licenses, "application_license_assigned": False,
                                               "application_license_note": "No application license is invented by this collector."})
        normalize(stage, epoch)
        output.mkdir(parents=True, exist_ok=True, mode=0o755)
        for path in stage.iterdir():
            target = output / path.name
            shutil.copytree(path, target) if path.is_dir() else shutil.copyfile(path, target)
        normalize(output, epoch)
    print(json.dumps({"output": str(output), "commit": pin["TINC_UPSTREAM_COMMIT"], "go_licenses": licenses}))


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, RuntimeError) as error:
        raise SystemExit("Source collection failed: " + str(error)) from None
