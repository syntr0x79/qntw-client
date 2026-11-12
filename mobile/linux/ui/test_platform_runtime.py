"""OS selection tests without GTK, network, subprocesses or a real vault."""
import ctypes
from pathlib import Path
import unittest
from unittest.mock import patch

import platform_runtime as runtime
from credential_windows import (CREDENTIALW, CRED_PERSIST_LOCAL_MACHINE,
                                CRED_TYPE_GENERIC, ERROR_NOT_FOUND, LPBYTE,
                                MAX_BLOB_BYTES, TARGET, WindowsCredentialVault)


class PlatformRuntimeTests(unittest.TestCase):
    def test_linux_defaults_preserve_layout_and_process_options(self):
        with patch.object(runtime.sys, "platform", "linux"), patch.dict(runtime.os.environ, {}, clear=True):
            self.assertEqual(runtime.platform_label(), "Linux")
            self.assertEqual(runtime.helper_path(), str(Path(runtime.__file__).resolve().parent.parent / "build/quantway-helper"))
            self.assertEqual(runtime.helper_process_options(), {})
            self.assertTrue(str(runtime.brand_image()).endswith("android/app/src/main/res/drawable/quantway_brand.png"))

    def test_windows_paths_hidden_utf8_helpers_and_packaged_assets(self):
        with patch.object(runtime.sys, "platform", "win32"), patch.dict(runtime.os.environ, {}, clear=True):
            self.assertEqual(runtime.platform_label(), "Windows")
            self.assertTrue(runtime.helper_path().endswith("bin/quantway-helper.exe"))
            self.assertEqual(runtime.helper_process_options(), {"creationflags": 0x08000000, "encoding": "utf-8"})
            with patch.dict(runtime.os.environ, {"QUANTWAY_HELPER": "/fixture/Program Files/Quantway/bin/quantway-helper.exe", "QUANTWAY_ASSET_DIR": "/fixture/Program Files/Quantway/assets"}):
                self.assertEqual(runtime.helper_path(), "/fixture/Program Files/Quantway/bin/quantway-helper.exe")
                self.assertEqual(runtime.brand_image(), Path("/fixture/Program Files/Quantway/assets/quantway_brand.png"))


class CredAPI:
    def __init__(self):
        self.raw = b"qw_session=offline-windows-fixture"
        self.calls = []
        self.return_type = CRED_TYPE_GENERIC
        self.return_target = TARGET
        self.read_size = None
        self.read_ok = self.write_ok = self.delete_ok = True

    def CredReadW(self, target, kind, flags, result):
        self.calls.append(("read", target, kind, flags))
        if not self.read_ok:
            return 0
        self.buffer = (ctypes.c_ubyte * len(self.raw)).from_buffer_copy(self.raw)
        self.credential = CREDENTIALW()
        self.credential.Type = self.return_type
        self.credential.TargetName = self.return_target
        self.credential.CredentialBlobSize = len(self.raw) if self.read_size is None else self.read_size
        self.credential.CredentialBlob = ctypes.cast(self.buffer, LPBYTE)
        ctypes.cast(result, ctypes.POINTER(ctypes.POINTER(CREDENTIALW)))[0] = ctypes.pointer(self.credential)
        return 1

    def CredWriteW(self, credential, flags):
        self.saved_arg = credential  # retain backing ctypes objects for zeroing assertion
        value = ctypes.cast(credential, ctypes.POINTER(CREDENTIALW)).contents
        self.written = {"target": value.TargetName, "type": value.Type, "persist": value.Persist,
                        "bytes": ctypes.string_at(value.CredentialBlob, value.CredentialBlobSize),
                        "attributes": value.AttributeCount, "flags": flags}
        self.calls.append(("write",))
        return int(self.write_ok)

    def CredDeleteW(self, target, kind, flags):
        self.calls.append(("delete", target, kind, flags))
        return int(self.delete_ok)

    def CredFree(self, pointer):
        self.calls.append(("free",))


class WindowsCredentialAPITests(unittest.TestCase):
    def vault(self):
        api = CredAPI()
        return WindowsCredentialVault(api=api), api

    def test_win64_abi_uses_dword_and_pointer_sized_offsets(self):
        if ctypes.sizeof(ctypes.c_void_p) == 8:
            self.assertEqual(ctypes.sizeof(CREDENTIALW), 80)
            self.assertEqual(CREDENTIALW.CredentialBlob.offset, 40)
            self.assertEqual(CREDENTIALW.UserName.offset, 72)

    def test_read_is_scoped_and_frees_os_allocation(self):
        vault, api = self.vault()
        self.assertEqual(vault.read(), api.raw)
        self.assertEqual(api.calls, [("read", TARGET, CRED_TYPE_GENERIC, 0), ("free",)])

    def test_invalid_blob_metadata_is_rejected_and_still_freed(self):
        for field, value in [("read_size", 0), ("read_size", MAX_BLOB_BYTES + 1),
                             ("return_type", 2), ("return_target", "other-app")]:
            vault, api = self.vault()
            setattr(api, field, value)
            with self.subTest(field=field, value=value), self.assertRaises(OSError):
                vault.read()
            self.assertEqual(api.calls[-1], ("free",))

    def test_write_overwrites_only_app_target_and_zeroes_temporary_buffer(self):
        vault, api = self.vault()
        raw = b"qw_session=offline-windows-fixture"
        vault.write(raw)
        self.assertEqual(api.written, {"target": TARGET, "type": CRED_TYPE_GENERIC, "persist": CRED_PERSIST_LOCAL_MACHINE,
                                       "bytes": raw, "attributes": 0, "flags": 0})
        value = ctypes.cast(api.saved_arg, ctypes.POINTER(CREDENTIALW)).contents
        self.assertEqual(ctypes.string_at(value.CredentialBlob, value.CredentialBlobSize), b"\0" * len(raw))
        for invalid in [b"", b"x" * (MAX_BLOB_BYTES + 1), "not-bytes"]:
            with self.subTest(value=type(invalid).__name__), self.assertRaises(OSError):
                vault.write(invalid)
        self.assertEqual(api.calls, [("write",)])

    def test_not_found_is_empty_and_delete_is_idempotent(self):
        vault, api = self.vault()
        api.read_ok = api.delete_ok = False
        with patch.object(vault, "_error", return_value=OSError(ERROR_NOT_FOUND, "fixture")):
            self.assertIsNone(vault.read())
            vault.delete()
        self.assertEqual(api.calls, [("read", TARGET, CRED_TYPE_GENERIC, 0), ("delete", TARGET, CRED_TYPE_GENERIC, 0)])

    def test_other_api_errors_do_not_become_success(self):
        vault, api = self.vault()
        api.read_ok = api.write_ok = api.delete_ok = False
        with patch.object(vault, "_error", return_value=OSError(5, "fixture access denied")):
            for operation in [vault.read, vault.delete, lambda: vault.write(api.raw)]:
                with self.assertRaises(OSError):
                    operation()


if __name__ == "__main__":
    unittest.main(verbosity=2)
