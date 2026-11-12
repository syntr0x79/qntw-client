#!/usr/bin/env python3
"""Verify the pinned Darwin Xray fork and prepare source/build provenance."""
import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import shlex
import subprocess
import tempfile
import zipfile

MOBILE = Path(__file__).resolve().parents[1]
NATIVE = MOBILE / "native/xray"


def digest(data):
    return hashlib.sha256(data).hexdigest()


def verified_archive(module, version, expected_sum):
    info = json.loads(subprocess.check_output(
        ["go", "mod", "download", "-json", module + "@" + version], cwd=MOBILE, text=True))
    if info.get("Error") or info.get("Path") != module or info.get("Version") != version:
        raise RuntimeError("unexpected Xray module")
    archive = zipfile.ZipFile(info["Zip"])
    checksum = hashlib.sha256()
    names = sorted(n for n in archive.namelist() if not n.endswith("/"))
    prefix = module + "@" + version + "/"
    for name in names:
        if not name.startswith(prefix) or any(p in ("..", ".") for p in Path(name).parts):
            raise RuntimeError("unsafe Xray module archive path")
        checksum.update((digest(archive.read(name)) + "  " + name + "\n").encode())
    actual = "h1:" + base64.b64encode(checksum.digest()).decode()
    if actual != expected_sum or info.get("Sum") != expected_sum:
        raise RuntimeError("Xray module archive checksum mismatch")
    return info, archive, prefix


def prepare(output=None):
    pin = json.loads((NATIVE / "source-pin.json").read_text())
    selected = json.loads(subprocess.check_output(
        ["go", "list", "-m", "-json", pin["module"]], cwd=MOBILE, text=True))
    replace = selected.get("Replace", {})
    if replace.get("Path") != pin["fork_module"] or replace.get("Version") != pin["fork_version"]:
        raise RuntimeError("mobile/go.mod must select the reviewed pinned Xray fork")
    upstream_info, upstream, prefix = verified_archive(pin["module"], pin["version"], pin["module_sum"])
    source = upstream.read(prefix + pin["source_file"])
    upstream.close()
    if digest(source) != pin["source_sha256"]:
        raise RuntimeError("upstream Darwin source differs from reviewed pin")
    info, fork, prefix = verified_archive(pin["fork_module"], pin["fork_version"], pin["fork_sum"])
    origin_hash = info.get("Origin", {}).get("Hash")
    if origin_hash and origin_hash != pin["fork_commit"]:
        raise RuntimeError("fork commit differs from reviewed pin")
    patched = fork.read(prefix + pin["source_file"])
    tests = fork.read(prefix + pin["test_overlay_file"])
    license_data = fork.read(prefix + "LICENSE")
    fork.close()
    if digest(patched) != pin["patched_source_sha256"]:
        raise RuntimeError("fork Darwin source differs from reviewed fix")
    patch = (NATIVE / pin["patch"]).read_bytes()
    reviewed_tests = (NATIVE / pin["test"]).read_bytes()
    if tests != reviewed_tests:
        raise RuntimeError("fork regression tests differ from reviewed source")
    with tempfile.TemporaryDirectory(prefix="quantway-xray-review-") as work:
        target = Path(work) / pin["source_file"]
        target.parent.mkdir(parents=True)
        target.write_bytes(source)
        patch_file = Path(work) / "reviewed.patch"
        patch_file.write_bytes(patch)
        subprocess.run(["git", "apply", "--check", str(patch_file)], cwd=work, check=True, capture_output=True)
        subprocess.run(["git", "apply", str(patch_file)], cwd=work, check=True, capture_output=True)
        if target.read_bytes() != patched:
            raise RuntimeError("reviewed patch does not produce the pinned fork source")
    identity = {"module": pin["module"], "version": pin["version"],
                "fork_module": pin["fork_module"], "fork_version": pin["fork_version"],
                "fork_commit": pin["fork_commit"], "fork_sum": pin["fork_sum"],
                "upstream_source_sha256": digest(source), "patch_sha256": digest(patch),
                "patched_source_sha256": digest(patched), "regression_test_sha256": digest(tests)}
    fingerprint = digest(json.dumps(identity, sort_keys=True).encode())
    if output is None:
        output = Path("/private/tmp/quantway-xray-darwin") / fingerprint[:24]
    output = Path(output).absolute()
    if output.is_symlink():
        raise RuntimeError("receipt directory may not be a symlink")
    output.mkdir(parents=True, mode=0o700, exist_ok=True)
    os.chmod(output, 0o700)
    manifest = {**identity, "source_fingerprint": fingerprint,
                "module_directory": info["Dir"], "overlay_directory": str(output),
                "integration": "ordinary Go module replacement; no GOFLAGS override or module-cache mutation",
                "interface_policy": "caller-managed supplied interface; Xray owns its CLOEXEC FD duplicate"}
    files = {"upstream-tun_darwin.go": source, "tun_darwin.go": patched,
             "0001-own-duplicate-of-borrowed-darwin-tun.patch": patch,
             "quantway_fd_ownership_darwin_test.go": tests, "LICENSE": license_data,
             "MANIFEST.json": (json.dumps(manifest, indent=2) + "\n").encode()}
    with tempfile.TemporaryDirectory(prefix=".prepare-", dir=output) as work:
        for name, data in files.items():
            path = Path(work) / name
            path.write_bytes(data)
            os.replace(path, output / name)
    return manifest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, default=os.environ.get("QUANTWAY_XRAY_OVERLAY_DIR"))
    parser.add_argument("--exec", dest="command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    # A hidden overlay/modfile would defeat the ordinary fork replacement.
    flags = shlex.split(os.environ.get("GOFLAGS", ""))
    if any(f in ("-overlay", "-modfile") or f.startswith(("-overlay=", "-modfile=")) for f in flags):
        raise RuntimeError("remove external overlay/modfile flags before the pinned Xray build")
    manifest = prepare(args.output)
    if args.command is None:
        print(json.dumps(manifest, indent=2))
        return
    if not args.command:
        parser.error("--exec needs a command")
    env = dict(os.environ)
    env["QUANTWAY_XRAY_OVERLAY_DIR"] = manifest["overlay_directory"]
    env["QUANTWAY_XRAY_SOURCE_FINGERPRINT"] = manifest["source_fingerprint"]
    env["QUANTWAY_XRAY_MANIFEST"] = str(Path(manifest["overlay_directory"]) / "MANIFEST.json")
    os.execvpe(args.command[0], args.command, env)


if __name__ == "__main__":
    main()
