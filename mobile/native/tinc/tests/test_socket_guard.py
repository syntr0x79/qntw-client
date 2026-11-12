"""Exercise descriptor passing and failure behavior against a real Unix broker.

The broker here deliberately only inspects descriptors; Android protect/Network
binding must also be tested in VpnService, not inferred from this host test.
"""
import array
import ctypes
import errno
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import threading
import time
import unittest

NATIVE = Path(__file__).resolve().parents[1]


class SocketGuardTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.build = tempfile.TemporaryDirectory(prefix="qntw-guard-")
        cls.library = Path(cls.build.name) / "guard.so"
        subprocess.run(["cc", "-std=c17", "-Wall", "-Wextra", "-Werror", "-shared", "-fPIC",
                        str(NATIVE / "socket_guard.c"), "-o", str(cls.library)], check=True)
        cls.guard = ctypes.CDLL(str(cls.library), use_errno=True)
        cls.guard.qntw_guarded_socket.argtypes = [ctypes.c_int] * 3
        cls.guard.qntw_guarded_socket.restype = ctypes.c_int
        cls.guard.qntw_numeric_getaddrinfo.argtypes = [ctypes.c_char_p, ctypes.c_char_p,
                                                     ctypes.c_void_p, ctypes.c_void_p]
        cls.guard.qntw_numeric_getaddrinfo.restype = ctypes.c_int

    @classmethod
    def tearDownClass(cls):
        cls.build.cleanup()

    def setUp(self):
        self.previous = os.environ.pop("TINC_SOCKET_GUARD", None)
        self.directory = tempfile.TemporaryDirectory(prefix="qg-", dir="/tmp")
        self.path = str(Path(self.directory.name) / "guard.sock")
        self.errors = []
        self.seen = []

    def tearDown(self):
        if self.previous is None:
            os.environ.pop("TINC_SOCKET_GUARD", None)
        else:
            os.environ["TINC_SOCKET_GUARD"] = self.previous
        self.directory.cleanup()

    def broker(self, count=1, response=b"\x00", delay=0):
        server = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        server.bind(self.path)
        server.listen()
        server.settimeout(10)
        os.environ["TINC_SOCKET_GUARD"] = self.path

        def run():
            try:
                for _ in range(count):
                    client, _ = server.accept()
                    with client:
                        data, ancillary, flags, _ = client.recvmsg(1, socket.CMSG_SPACE(4))
                        self.assertEqual(data, b"\x01")
                        self.assertEqual(flags, 0)
                        self.assertEqual(len(ancillary), 1)
                        level, kind, body = ancillary[0]
                        self.assertEqual((level, kind), (socket.SOL_SOCKET, socket.SCM_RIGHTS))
                        descriptors = array.array("i", body)
                        self.assertEqual(len(descriptors), 1)
                        with socket.socket(fileno=descriptors[0]) as duplicate:
                            self.seen.append((duplicate.family, duplicate.type, duplicate.getsockname()))
                            self.assertEqual(duplicate.getsockname()[1], 0)
                            with self.assertRaises(OSError):
                                duplicate.getpeername()
                            if delay:
                                time.sleep(delay)
                            if response is not None:
                                try:
                                    client.sendall(response)
                                except BrokenPipeError:
                                    if not delay:
                                        raise
            except Exception as error:
                self.errors.append(error)
            finally:
                server.close()

        thread = threading.Thread(target=run)
        thread.start()
        return thread

    def finish(self, thread):
        thread.join(8)
        self.assertFalse(thread.is_alive())
        if self.errors:
            raise self.errors[0]

    def test_ipv4_tcp_and_udp_are_unbound_before_approval(self):
        thread = self.broker(count=2)
        for kind in (socket.SOCK_STREAM, socket.SOCK_DGRAM):
            descriptor = self.guard.qntw_guarded_socket(socket.AF_INET, kind, 0)
            self.assertGreaterEqual(descriptor, 0)
            os.close(descriptor)
        self.finish(thread)
        self.assertEqual(len(self.seen), 2)

    def test_ipv6_is_also_guarded(self):
        thread = self.broker()
        descriptor = self.guard.qntw_guarded_socket(socket.AF_INET6, socket.SOCK_DGRAM, 0)
        self.assertGreaterEqual(descriptor, 0)
        os.close(descriptor)
        self.finish(thread)
        self.assertEqual(self.seen[0][0], socket.AF_INET6)

    def test_missing_or_failed_broker_cannot_return_network_socket(self):
        for callback in (None, "/tmp/qntw-no-such-broker.sock", "relative.sock"):
            if callback is None:
                os.environ.pop("TINC_SOCKET_GUARD", None)
            else:
                os.environ["TINC_SOCKET_GUARD"] = callback
            self.assertEqual(self.guard.qntw_guarded_socket(socket.AF_INET, socket.SOCK_DGRAM, 0), -1)

    def test_rejection_and_disconnect_fail_closed(self):
        for response in (b"\x01", None):
            if Path(self.path).exists():
                Path(self.path).unlink()
            thread = self.broker(response=response)
            self.assertEqual(self.guard.qntw_guarded_socket(socket.AF_INET, socket.SOCK_STREAM, 0), -1)
            self.finish(thread)

    def test_callback_timeout_is_bounded(self):
        thread = self.broker(delay=5.3)
        start = time.monotonic()
        self.assertEqual(self.guard.qntw_guarded_socket(socket.AF_INET, socket.SOCK_DGRAM, 0), -1)
        self.assertEqual(ctypes.get_errno(), errno.ETIMEDOUT)
        self.assertLess(time.monotonic() - start, 6.2)
        self.finish(thread)

    def test_local_socket_needs_no_network_approval(self):
        descriptor = self.guard.qntw_guarded_socket(socket.AF_UNIX, socket.SOCK_DGRAM, 0)
        self.assertGreaterEqual(descriptor, 0)
        os.close(descriptor)

    def test_symbolic_hostname_or_service_cannot_use_system_dns(self):
        result = ctypes.c_void_p()
        for host, port in ((b"example.com", b"7443"), (b"127.0.0.1", b"https")):
            self.assertNotEqual(self.guard.qntw_numeric_getaddrinfo(host, port, None, ctypes.byref(result)), 0)


if __name__ == "__main__":
    unittest.main()
