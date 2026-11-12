"""Packaging must reject wrong profiles and modified native source/binaries."""
import hashlib
import importlib.util
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location("verify", Path(__file__).with_name("verify-tinc-artifacts.py"))
verifier = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verifier)


class NativeArtifactsTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.path = Path(self.tmp.name)
        for name in ("tinc", "tincd", "COPYING", "tinc-source.tar.gz"):
            (self.path / name).write_bytes(name.encode())
        (self.path / "BUILD-INFO.txt").write_text(
            "fork_commit=abc\ntarget=host-Darwin-arm64\nprofile=supervised-client\n"
            "socket_guard=true\nclient_hooks=false\ncrypto=nolegacy\n"
            "udp_batch_size=1\nudp_offload=false\n")
        self.checksums()

    def checksums(self):
        names = ("tinc", "tincd", "COPYING", "BUILD-INFO.txt", "tinc-source.tar.gz")
        (self.path / "SHA256SUMS").write_text("".join(
            f"{hashlib.sha256((self.path / n).read_bytes()).hexdigest()}  {n}\n" for n in names))

    def test_valid(self):
        verifier.verify(self.path, "abc", "host-Darwin-arm64")

    def test_tampered_binary_and_source(self):
        for name in ("tincd", "tinc-source.tar.gz"):
            with self.subTest(name=name):
                (self.path / name).write_bytes(b"changed")
                with self.assertRaisesRegex(ValueError, "checksum"):
                    verifier.verify(self.path, "abc", "host-Darwin-arm64")
                self.checksums()

    def test_hooks_and_commit(self):
        with self.assertRaisesRegex(ValueError, "fork_commit"):
            verifier.verify(self.path, "other", "host-Darwin-arm64")
        info = self.path / "BUILD-INFO.txt"
        info.write_text(info.read_text().replace("client_hooks=false", "client_hooks=true"))
        self.checksums()
        with self.assertRaisesRegex(ValueError, "client_hooks"):
            verifier.verify(self.path, "abc", "host-Darwin-arm64")

    def test_unsafe_name(self):
        with (self.path / "SHA256SUMS").open("a") as f:
            f.write("0" * 64 + "  ../outside\n")
        with self.assertRaisesRegex(ValueError, "unexpected"):
            verifier.verify(self.path, "abc", "host-Darwin-arm64")


if __name__ == "__main__":
    unittest.main()
