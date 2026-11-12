#!/usr/bin/env python3
"""Reject a stale, tampered or incorrectly configured native client bundle."""
import argparse
import hashlib
from pathlib import Path


def verify(directory: Path, commit: str, target: str) -> dict:
    required = {"tinc", "tincd", "COPYING", "BUILD-INFO.txt", "tinc-source.tar.gz"}
    checksums = {}
    for line in (directory / "SHA256SUMS").read_text().splitlines():
        digest, name = line.split(None, 1)
        name = name.lstrip(" *")
        if name not in required or name in checksums:
            raise ValueError("unexpected or duplicate native artifact")
        path = directory / name
        if path.is_symlink() or not path.is_file():
            raise ValueError("native artifact must be a regular file")
        if hashlib.sha256(path.read_bytes()).hexdigest() != digest:
            raise ValueError(f"native artifact checksum mismatch: {name}")
        checksums[name] = digest
    if checksums.keys() != required:
        raise ValueError("incomplete native artifacts")
    fields = {}
    for line in (directory / "BUILD-INFO.txt").read_text().splitlines():
        if "=" in line:
            key, value = line.split("=", 1)
            if key in fields:
                raise ValueError("duplicate native build metadata")
            fields[key] = value
    for key, value in {
        "fork_commit": commit, "target": target, "profile": "supervised-client",
        "socket_guard": "true", "client_hooks": "false", "crypto": "nolegacy",
        "udp_batch_size": "1", "udp_offload": "false",
    }.items():
        if fields.get(key) != value:
            raise ValueError(f"unexpected native build metadata: {key}")
    return fields


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("directory", type=Path)
    parser.add_argument("--commit", required=True)
    parser.add_argument("--target", required=True)
    args = parser.parse_args()
    verify(args.directory, args.commit, args.target)
    print("Native fork artifacts and supervised-client profile verified")
