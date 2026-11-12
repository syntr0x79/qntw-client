#!/usr/bin/env python3
"""Secret Service protocol tests using only an injected in-memory DBus peer."""
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from gi.repository import Gio, GLib

from session_store import (RestorePolicy, SecretSessionStore, SessionStoreCancelled,
                           SessionStoreError, valid_session_cookie)

COOKIE = "qw_session=offline-credential-fixture"
ROOT = SecretSessionStore.ROOT
ITEM = ROOT + "/collection/login/fixture"
DUPLICATE = ROOT + "/collection/login/duplicate"
COLLECTION = ROOT + "/collection/login"
SESSION = ROOT + "/session/fixture"


class Reply:
    def __init__(self, value):
        self.value = value

    def unpack(self):
        return self.value


class FakeConnection:
    """Reject every unplanned call, and retain real GLib variants for assertions."""
    def __init__(self, *replies):
        self.replies = list(replies)
        self.calls = []

    def call_sync(self, name, path, interface, method, parameters, reply_type, flags, timeout, cancel):
        self.calls.append((name, path, interface, method, parameters, cancel))
        if not self.replies:
            raise AssertionError("Unexpected Secret Service method: " + method)
        expected_method, value = self.replies.pop(0)
        if method != expected_method:
            raise AssertionError((expected_method, method))
        if isinstance(value, Exception):
            raise value
        return Reply(value)

    def assert_finished(self, case):
        case.assertEqual(self.replies, [])


def secret(cookie=COOKIE, session=SESSION, parameters=None):
    return (session, parameters or [], list(cookie.encode("utf-8")), "text/plain; charset=utf-8")


class SecretStoreTests(unittest.TestCase):
    def setUp(self):
        # If a test forgets injection, it fails before touching any real bus.
        self.bus_guard = patch("session_store.Gio.bus_get_sync", side_effect=AssertionError("Host DBus forbidden"))
        self.bus_guard.start()

    def tearDown(self):
        self.bus_guard.stop()

    def store(self, *replies):
        connection = FakeConnection(*replies)
        return SecretSessionStore(connection=connection), connection

    def test_cookie_validation_rejects_codes_headers_and_subscription_urls(self):
        self.assertTrue(valid_session_cookie(COOKIE))
        for value in (None, "", "qw_session=", "a-code", "https://example.invalid/sub",
                      COOKIE + "; other=x", COOKIE + "\r\nX-Foo:x", COOKIE + " ",
                      'qw_session="quoted"', "qw_session=" + "x" * 4096):
            with self.subTest(value=repr(value)[:60]):
                self.assertFalse(valid_session_cookie(value))
        store, connection = self.store()
        with self.assertRaises(SessionStoreError):
            store.save("https://example.invalid/sub")
        self.assertEqual(connection.calls, [])

    def test_missing_item_returns_none_without_opening_session(self):
        store, connection = self.store(("SearchItems", ([], [])))
        self.assertIsNone(store.load())
        call = connection.calls[0]
        self.assertEqual(call[:4], (store.NAME, ROOT, store.SERVICE, "SearchItems"))
        self.assertEqual(call[4].get_type_string(), "(a{ss})")
        self.assertEqual(call[4].unpack(), (store.ATTRIBUTES,))
        connection.assert_finished(self)

    def test_load_unlocks_and_reads_only_scoped_item_then_closes_session(self):
        store, connection = self.store(("SearchItems", ([], [ITEM])), ("Unlock", ([ITEM], "/")),
                                       ("OpenSession", ("", SESSION)), ("GetSecret", (secret(),)), ("Close", ()))
        self.assertEqual(store.load(), COOKIE)
        self.assertEqual(connection.calls[1][4].unpack(), ([ITEM],))
        self.assertEqual(connection.calls[2][4].get_type_string(), "(sv)")
        self.assertEqual(connection.calls[2][4].unpack(), ("plain", ""))
        self.assertEqual(connection.calls[3][1], ITEM)
        self.assertEqual(connection.calls[3][4].unpack(), (SESSION,))
        connection.assert_finished(self)

    def test_invalid_secret_and_duplicate_records_never_restore(self):
        store, connection = self.store(("SearchItems", ([ITEM], [DUPLICATE])))
        with self.assertRaises(SessionStoreError):
            store.load()
        connection.assert_finished(self)
        for value in (secret("bad-cookie"), secret(session=ROOT + "/session/other"),
                      secret(parameters=[1]), (SESSION, [], [255], "text/plain")):
            with self.subTest(secret=value):
                store, connection = self.store(("SearchItems", ([ITEM], [])), ("Unlock", ([ITEM], "/")),
                                               ("OpenSession", ("", SESSION)), ("GetSecret", (value,)), ("Close", ()))
                with self.assertRaises(SessionStoreError):
                    store.load()
                connection.assert_finished(self)

    def test_save_creates_one_replaceable_secret_without_credential_attributes(self):
        store, connection = self.store(("SearchItems", ([], [])), ("ReadAlias", (COLLECTION,)),
                                       ("Unlock", ([COLLECTION], "/")), ("OpenSession", ("", SESSION)),
                                       ("CreateItem", (ITEM, "/")), ("Close", ()))
        store.save(COOKIE)
        create = connection.calls[4]
        self.assertEqual(create[4].get_type_string(), "(a{sv}(oayays)b)")
        properties, stored_secret, replace = create[4].unpack()
        self.assertEqual(properties["org.freedesktop.Secret.Item.Attributes"], store.ATTRIBUTES)
        self.assertNotIn(COOKIE, repr(properties))
        self.assertEqual(bytes(stored_secret[2]).decode(), COOKIE)
        self.assertEqual(stored_secret[:2], (SESSION, []))
        self.assertTrue(replace)
        connection.assert_finished(self)

    def test_save_updates_existing_item_and_removes_only_scoped_duplicates(self):
        store, connection = self.store(("SearchItems", ([ITEM, ITEM], [DUPLICATE])),
                                       ("Unlock", ([ITEM, DUPLICATE], "/")),
                                       ("OpenSession", ("", SESSION)), ("SetSecret", ()),
                                       ("Delete", ("/",)), ("Close", ()))
        store.save(COOKIE)
        self.assertEqual(connection.calls[3][1], ITEM)
        self.assertEqual(connection.calls[4][1], DUPLICATE)
        self.assertEqual(bytes(connection.calls[3][4].unpack()[0][2]).decode(), COOKIE)
        connection.assert_finished(self)

    def test_create_default_collection_and_prompt_paths(self):
        store, connection = self.store(("SearchItems", ([], [])), ("ReadAlias", ("/",)),
                                       ("CreateCollection", ("/", ROOT + "/prompt/create")),
                                       ("Unlock", ([COLLECTION], "/")), ("OpenSession", ("", SESSION)),
                                       ("CreateItem", (ITEM, "/")), ("Close", ()))
        with patch.object(store, "_prompt", side_effect=[COLLECTION, None, None]) as prompt:
            store.save(COOKIE)
        self.assertEqual(connection.calls[3][4].unpack(), ([COLLECTION],))
        self.assertEqual(prompt.call_args_list[0].args[0], ROOT + "/prompt/create")
        connection.assert_finished(self)

    def test_cancelled_prompt_and_service_failure_never_report_saved(self):
        store, connection = self.store(("SearchItems", ([], [])), ("ReadAlias", (COLLECTION,)),
                                       ("Unlock", ([COLLECTION], ROOT + "/prompt/unlock")))
        with patch.object(store, "_prompt", side_effect=SessionStoreCancelled("cancel")):
            with self.assertRaises(SessionStoreCancelled):
                store.save(COOKIE)
        self.assertNotIn("CreateItem", [call[3] for call in connection.calls])
        connection.assert_finished(self)
        store, connection = self.store(("SearchItems", GLib.Error("unavailable")))
        with self.assertRaises(SessionStoreError):
            store.save(COOKIE)
        connection.assert_finished(self)

    def test_prompt_timeout_dismisses_and_unsubscribes(self):
        store, connection = self.store(("Prompt", ()), ("Dismiss", ()))
        connection.signal_subscribe = lambda *args: 17
        unsubscribed = []
        connection.signal_unsubscribe = unsubscribed.append
        store.prompt_timeout = 0
        with self.assertRaises(SessionStoreCancelled):
            store._prompt(ROOT + "/prompt/locked")
        self.assertEqual(unsubscribed, [17])
        connection.assert_finished(self)

    def test_completed_prompt_unwraps_result_and_dismissal_never_succeeds(self):
        for dismissed in (False, True):
            with self.subTest(dismissed=dismissed):
                store, connection = self.store(("Prompt", ()))
                callbacks, unsubscribed = [], []
                def subscribe(*args):
                    callbacks.append(args[-1])
                    return 23
                connection.signal_subscribe = subscribe
                connection.signal_unsubscribe = unsubscribed.append
                original = connection.call_sync
                def call(*args):
                    reply = original(*args)
                    callbacks[0](connection, store.NAME, ROOT + "/prompt/create", "org.freedesktop.Secret.Prompt",
                                 "Completed", GLib.Variant("(bv)", (dismissed, GLib.Variant("o", ITEM))))
                    return reply
                connection.call_sync = call
                if dismissed:
                    with self.assertRaises(SessionStoreCancelled):
                        store._prompt(ROOT + "/prompt/create")
                else:
                    self.assertEqual(store._prompt(ROOT + "/prompt/create"), ITEM)
                self.assertEqual(unsubscribed, [23])
                connection.assert_finished(self)

    def test_delete_is_idempotent_and_never_looks_up_other_apps(self):
        store, connection = self.store(("SearchItems", ([], [])))
        store.delete()
        connection.assert_finished(self)
        store, connection = self.store(("SearchItems", ([ITEM], [DUPLICATE])),
                                       ("Unlock", ([ITEM, DUPLICATE], "/")),
                                       ("Delete", ("/",)), ("Delete", ("/",)))
        store.delete()
        self.assertEqual([call[1] for call in connection.calls if call[3] == "Delete"], [ITEM, DUPLICATE])
        self.assertEqual(connection.calls[0][4].unpack()[0], store.ATTRIBUTES)
        connection.assert_finished(self)

    def test_cancelled_operation_never_starts_bus_request(self):
        store, connection = self.store()
        cancel = Gio.Cancellable()
        cancel.cancel()
        for operation in (store.load, store.delete, lambda value: store.save(COOKIE, value)):
            with self.assertRaises(SessionStoreCancelled):
                operation(cancel)
        self.assertEqual(connection.calls, [])

    def test_restore_policy_contains_only_empty_private_logout_marker(self):
        with tempfile.TemporaryDirectory() as directory:
            policy = RestorePolicy(Path(directory) / "quantway")
            self.assertFalse(policy.disabled())
            policy.disable()
            self.assertTrue(policy.disabled())
            self.assertEqual(policy.path.read_bytes(), b"")
            self.assertEqual(policy.path.stat().st_mode & 0o777, 0o600)
            self.assertEqual(policy.directory.stat().st_mode & 0o777, 0o700)
            policy.enable()
            self.assertFalse(policy.disabled())
            policy.enable()

    def test_restore_policy_fails_closed_for_unreadable_marker_or_dangling_symlink(self):
        with tempfile.TemporaryDirectory() as directory:
            policy = RestorePolicy(directory)
            with patch.object(Path, "lstat", side_effect=PermissionError("fixture inaccessible")):
                self.assertTrue(policy.disabled())
            self.assertFalse(policy.disabled())
            policy.path.symlink_to(Path(directory) / "missing-target")
            self.assertTrue(policy.disabled())
            with self.assertRaises(OSError):
                policy.disable()
            self.assertFalse((Path(directory) / "missing-target").exists())
            policy.enable()
            self.assertFalse(policy.disabled())


class WindowsStoreTests(unittest.TestCase):
    def vault(self, raw=COOKIE.encode()):
        from unittest.mock import Mock
        vault = Mock()
        vault.read.return_value = raw
        return vault

    def test_platform_factory_is_lazy_and_selects_user_vault(self):
        from session_store import WindowsSessionStore, create_session_store
        with patch("session_store.is_windows", return_value=True):
            store = create_session_store()
            self.assertIsInstance(store, WindowsSessionStore)
            self.assertIsNone(store.vault)
        with patch("session_store.is_windows", return_value=False):
            self.assertIsInstance(create_session_store(), SecretSessionStore)

    def test_roundtrip_is_cookie_only_and_no_bus_is_used(self):
        from session_store import WindowsSessionStore
        vault = self.vault()
        store = WindowsSessionStore(vault)
        with patch("session_store.Gio.bus_get_sync", side_effect=AssertionError("No Windows DBus")):
            self.assertEqual(store.load(), COOKIE)
            store.save(COOKIE)
            store.delete()
        vault.write.assert_called_once_with(COOKIE.encode())
        vault.delete.assert_called_once_with()

    def test_absent_corrupt_and_api_error_are_distinct(self):
        from session_store import WindowsSessionStore
        self.assertIsNone(WindowsSessionStore(self.vault(None)).load())
        for raw in [b"not-cookie", b"\xff", COOKIE.encode() + b"\n"]:
            with self.subTest(raw=raw), self.assertRaises(SessionStoreError):
                WindowsSessionStore(self.vault(raw)).load()
        vault = self.vault()
        vault.read.side_effect = OSError("offline vault")
        with self.assertRaises(SessionStoreError):
            WindowsSessionStore(vault).load()
        vault.write.side_effect = OSError("offline vault")
        with self.assertRaises(SessionStoreError):
            WindowsSessionStore(vault).save(COOKIE)

    def test_cancelled_operation_never_accesses_vault_and_invalid_cookie_never_writes(self):
        from session_store import WindowsSessionStore
        vault = self.vault()
        store = WindowsSessionStore(vault)
        cancel = Gio.Cancellable()
        cancel.cancel()
        for operation in [lambda: store.load(cancel), lambda: store.save(COOKIE, cancel), lambda: store.delete(cancel)]:
            with self.assertRaises(SessionStoreCancelled):
                operation()
        self.assertEqual(vault.mock_calls, [])
        with self.assertRaises(SessionStoreError):
            store.save("https://example.invalid/subscription")
        self.assertEqual(vault.mock_calls, [])

    def test_windows_logout_marker_avoids_unix_fchmod_and_directory_fsync(self):
        with tempfile.TemporaryDirectory() as directory, patch("session_store.is_windows", return_value=True):
            policy = RestorePolicy(directory)
            with patch("session_store.os.fchmod", side_effect=AssertionError("Windows has no fchmod")):
                policy.disable()
            self.assertTrue(policy.disabled())
            self.assertEqual(policy.path.read_bytes(), b"")
            self.assertEqual(list(Path(directory).glob(".session-autologin-*")), [])
            policy.enable()
            self.assertFalse(policy.disabled())


if __name__ == "__main__":
    unittest.main(verbosity=2)
