#!/usr/bin/env python3
"""Native GTK login persistence with in-memory secrets and deterministic workers."""
import threading
import unittest
from unittest.mock import Mock, patch

import gi
gi.require_version("Gtk", "4.0")
from gi.repository import Gio, GLib

from auth_persistence import ServiceFailure
from quantway import Quantway
from session_store import SessionStoreError

COOKIE = "qw_session=auth-offline-fixture"
NEW_COOKIE = "qw_session=auth-offline-rotated"
DEVICE = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
REAL_THREAD = threading.Thread


def account(cookie=COOKIE):
    return {"cookie": cookie, "identity": {"kind": "retail"}, "current_device": DEVICE,
            "device_link": "https://example.invalid/quantway-subscription",
            "account": {"subscription": {"status": "active", "days_left": 24, "devices": 3}}}


class MemoryPolicy:
    def __init__(self, disabled=False):
        self.value = disabled
        self.events = []

    def disabled(self):
        return self.value

    def disable(self):
        self.events.append("disable")
        self.value = True

    def enable(self):
        self.events.append("enable")
        self.value = False


class MemoryStore:
    def __init__(self, cookie=None):
        self.cookie = cookie
        self.events = []
        self.load_error = self.save_error = self.delete_error = False
        self.during_save = None

    def load(self, cancel=None):
        self.events.append(("load",))
        if self.load_error:
            raise SessionStoreError("fixture unavailable")
        return self.cookie

    def save(self, cookie, cancel=None):
        self.events.append(("save", cookie))
        if self.save_error:
            raise SessionStoreError("fixture unavailable")
        self.cookie = cookie
        if self.during_save:
            self.during_save()

    def delete(self, cancel=None):
        self.events.append(("delete",))
        if self.delete_error:
            raise SessionStoreError("fixture unavailable")
        self.cookie = None


class AuthPersistenceTests(unittest.TestCase):
    instance = 0

    def setUp(self):
        # Neither a missed store injection nor a missed helper mock can access
        # the host: every external boundary fails before I/O.
        self.bus_guard = patch("session_store.Gio.bus_get_sync", side_effect=AssertionError("Host DBus forbidden"))
        self.bus_guard.start()
        self.background = patch("auth_persistence.threading.Thread")
        self.thread = self.background.start()
        self.app = app = Quantway()
        app.session_store = self.store = MemoryStore()
        app.restore_policy = self.policy = MemoryPolicy()
        app.set_flags(Gio.ApplicationFlags.NON_UNIQUE)
        type(self).instance += 1
        app.set_application_id("link.quantway.authfixture.case" + str(type(self).instance))
        self.assertTrue(app.register(None))
        app.hold = Mock()
        app.release = Mock()
        self.helper_guard = patch.object(app, "call_helper", side_effect=AssertionError("Real helper forbidden"))
        self.helper_guard.start()
        self.stats_guard = patch.object(app, "refresh_account_stats")
        self.stats_guard.start()
        self.import_guard = patch.object(app, "queue_account_subscription", return_value=True)
        self.import_link = self.import_guard.start()
        app.activate()
        self.assertEqual(app._auth_operation, "load")

    def tearDown(self):
        self.app.closing = True
        self.app.close_auth_persistence()
        self.app.stop_support_poll()
        self.app.window.destroy()
        self.app.quit()
        self.import_guard.stop()
        self.stats_guard.stop()
        self.helper_guard.stop()
        self.background.stop()
        self.bus_guard.stop()

    def jobs(self):
        return [call.kwargs["target"] for call in self.thread.call_args_list]

    def run_job(self, index):
        # These workers execute synchronously in the main test thread. Real GTK
        # callbacks still run; no background work or DBus/main-loop race is hidden.
        def immediate(callback, *args):
            callback(*args)
            return 1
        with patch("auth_persistence.GLib.idle_add", side_effect=immediate):
            self.jobs()[index]()

    def complete_empty_startup(self):
        self.run_job(0)
        self.assertIsNone(self.app._auth_operation)
        self.thread.reset_mock()

    def test_valid_saved_cookie_restores_account_and_current_subscription(self):
        app = self.app
        self.store.cookie = COOKIE
        self.run_job(0)
        self.assertEqual(app._auth_cookie, COOKIE)
        self.assertEqual(app._auth_operation, "restore")
        with patch.object(app, "call_helper", return_value={"session": account()}) as helper:
            self.run_job(1)
        helper.assert_called_once_with("restore-session", {"cookie": COOKIE})
        self.assertEqual(app.session["cookie"], COOKIE)
        self.assertFalse(app.account_row.get_visible())
        self.import_link.assert_called_once_with(account()["device_link"])
        self.assertIn("Вход восстановлен", app.session_hint.get_text())
        self.assertFalse(app.session_retry.get_visible())
        self.assertEqual(self.store.events, [("load",)])

    def test_restore_keeps_external_selection_and_user_navigation(self):
        app = self.app
        self.store.cookie = COOKIE
        self.run_job(0)
        source = {"kind": "external", "id": "provider", "name": "Провайдер", "link": "https://example.invalid/external"}
        app.active_source = source
        app.subscription_generation += 1
        app.stack.set_visible_child_name("external")
        with patch.object(app, "call_helper", return_value={"session": account()}):
            self.run_job(1)
        self.assertEqual(app.session["cookie"], COOKIE)
        self.assertEqual(app.active_source, source)
        self.assertEqual(app.stack.get_visible_child_name(), "external")
        self.import_link.assert_not_called()

    def test_offline_restore_retains_cookie_and_retry_is_read_only(self):
        app = self.app
        self.store.cookie = COOKIE
        self.run_job(0)
        with patch.object(app, "call_helper", side_effect=ServiceFailure("offline", status=0)):
            self.run_job(1)
        self.assertIsNone(app.session)
        self.assertEqual(self.store.cookie, COOKIE)
        self.assertFalse(self.policy.value)
        self.assertEqual(app._auth_retry, "restore")
        self.assertTrue(app.session_retry.get_visible())
        self.assertIn("остался", app.session_hint.get_text())
        app.retry_saved_account()
        with patch.object(app, "call_helper", return_value={"session": account()}) as helper:
            self.run_job(2)
        helper.assert_called_once_with("restore-session", {"cookie": COOKIE})
        self.assertEqual(app.session["cookie"], COOKIE)
        self.assertNotIn(("delete",), self.store.events)

    def test_expired_saved_cookie_is_disabled_before_delete_and_cannot_restore(self):
        app = self.app
        self.store.cookie = COOKIE
        self.run_job(0)
        with patch.object(app, "call_helper", side_effect=ServiceFailure("expired", status=401)):
            self.run_job(1)
        self.assertIsNone(app.session)
        self.assertTrue(self.policy.value)
        self.assertIsNone(app._auth_cookie)
        self.assertIn("больше не действует", app.session_hint.get_text())
        self.run_job(2)
        self.assertIsNone(self.store.cookie)
        self.assertEqual(self.store.events[-1], ("delete",))

    def test_forbidden_policy_response_keeps_saved_cookie_for_retry(self):
        app = self.app
        self.store.cookie = COOKIE
        self.run_job(0)
        with patch.object(app, "call_helper", side_effect=ServiceFailure("policy/CSRF", status=403)):
            self.run_job(1)
        self.assertIsNone(app.session)
        self.assertEqual(self.store.cookie, COOKIE)
        self.assertFalse(self.policy.value)
        self.assertEqual(app._auth_retry, "restore")
        self.assertNotIn(("delete",), self.store.events)

    def test_store_load_failure_offers_retry_without_claiming_saved_or_network(self):
        app = self.app
        self.store.load_error = True
        self.run_job(0)
        self.assertIsNone(app.session)
        self.assertIsNone(app._auth_cookie)
        self.assertEqual(app._auth_retry, "load")
        self.assertIn("Хранилище", app.session_hint.get_text())
        self.assertEqual(len(self.jobs()), 1)
        self.store.load_error = False
        self.store.cookie = COOKIE
        app.retry_saved_account()
        self.run_job(1)
        self.assertEqual(app._auth_operation, "restore")

    def test_successful_login_is_exposed_only_after_keyring_acknowledges(self):
        app = self.app
        self.complete_empty_startup()
        app.login_button.set_sensitive(False)
        generation = app.account_generation
        app.login_received(account(), "", generation)
        self.assertIsNone(app.session)
        self.assertEqual(app._auth_operation, "save")
        self.assertIn("Сохраняем", app.session_hint.get_text())
        self.run_job(0)
        self.assertEqual(app.session["cookie"], COOKIE)
        self.assertEqual(self.store.cookie, COOKIE)
        self.assertEqual(app._auth_cookie, COOKIE)
        self.assertFalse(self.policy.value)
        self.assertIn("Вход сохранён", app.session_hint.get_text())
        self.assertTrue(app.login_button.get_sensitive())
        app.hold.assert_called_once()
        app.release.assert_called_once()

    def test_failed_save_keeps_session_usable_with_explicit_retry(self):
        app = self.app
        self.complete_empty_startup()
        self.store.save_error = True
        app.login_received(account(), "", app.account_generation)
        self.run_job(0)
        self.assertEqual(app.session["cookie"], COOKIE)
        self.assertIsNone(app._auth_cookie)
        self.assertEqual(app._auth_retry, "save")
        self.assertIn("только на этот запуск", app.session_hint.get_text())
        self.assertIn("не сохранён", app.status.get_text())
        self.store.save_error = False
        app.retry_saved_account()
        self.run_job(1)
        self.assertEqual(self.store.cookie, COOKIE)
        self.assertEqual(app._auth_cookie, COOKIE)
        self.assertFalse(app.session_retry.get_visible())

    def test_policy_write_failures_do_not_falsely_claim_persistence_or_safe_logout(self):
        app = self.app
        self.complete_empty_startup()
        self.policy.value = True
        with patch.object(self.policy, "enable", side_effect=OSError("fixture read-only settings")):
            app.login_received(account(), "", app.account_generation)
            self.run_job(0)
        self.assertEqual(self.store.cookie, COOKIE)
        self.assertTrue(self.policy.value)
        self.assertEqual(app._auth_retry, "save")
        self.assertIn("не удалось сохранить", app.session_hint.get_text())
        self.store.delete_error = True
        with patch.object(self.policy, "disable", side_effect=OSError("fixture read-only settings")):
            app.finish_account_logout()
            self.run_job(1)
        self.assertIsNone(app.session)
        self.assertIn("Не удалось отключить", app.session_hint.get_text())

    def test_offline_logout_immediately_clears_session_then_deletes_secret(self):
        app = self.app
        self.complete_empty_startup()
        self.store.cookie = COOKIE
        app.session = account()
        source = {"kind": "external", "id": "provider", "name": "Провайдер", "link": "https://example.invalid/sub"}
        app.active_source = source
        app.logout_account()
        self.assertIsNone(app.session)
        self.assertTrue(self.policy.value)
        self.assertEqual(app.active_source, source)
        self.run_job(0)
        with patch.object(app, "call_helper", side_effect=ServiceFailure("offline")) as helper:
            self.run_job(1)
        self.assertIsNone(self.store.cookie)
        helper.assert_called_once_with("cabinet", {"cookie": COOKIE, "method": "POST", "path": "logout", "body": {}})
        self.assertEqual(app.stack.get_visible_child_name(), "account")

    def test_unavailable_keyring_during_logout_stays_opted_out_next_startup(self):
        app = self.app
        self.complete_empty_startup()
        self.store.cookie = COOKIE
        self.store.delete_error = True
        app.session = account()
        app.finish_account_logout()
        self.run_job(0)
        self.assertIsNone(app.session)
        self.assertEqual(self.store.cookie, COOKIE)
        self.assertTrue(self.policy.value)
        self.thread.reset_mock()
        app._auth_started = False
        app.start_session_restore()
        self.assertEqual(self.jobs(), [])
        self.assertIsNone(app.session)

    def test_relogin_invalidates_pending_restore_and_older_login_callbacks(self):
        app = self.app
        self.store.cookie = COOKIE
        self.run_job(0)
        old_epoch, old_generation = app._auth_epoch, app.account_generation
        context = (app.subscription_generation, app.active_source, app.stack.get_visible_child_name())
        app.show_login_form(None)
        self.assertTrue(self.policy.value)
        app._account_restored(old_epoch, account(), False, context)
        app.login_received(account(), "", old_generation)
        self.assertIsNone(app.session)
        self.assertIsNone(app._auth_cookie)
        self.assertEqual(len(self.jobs()), 3)  # load, restore, forget; no stale save
        self.run_job(2)
        self.assertIsNone(self.store.cookie)

    def test_logout_during_pending_save_cannot_resurrect_secret_or_enable_restore(self):
        app = self.app
        self.complete_empty_startup()
        self.policy.value = True
        app.login_received(account(), "", app.account_generation)
        self.store.during_save = lambda: app.finish_account_logout()
        self.run_job(0)
        self.assertTrue(self.policy.value)
        self.assertNotIn("enable", self.policy.events)
        self.assertIsNone(app.session)
        self.assertIsNone(app._auth_cookie)
        self.run_job(1)
        self.assertIsNone(self.store.cookie)
        self.assertEqual(app.hold.call_count, app.release.call_count)

    def test_new_login_and_rotated_token_replace_only_cookie_and_reject_old_save(self):
        app = self.app
        self.complete_empty_startup()
        app.login_received(account(), "", app.account_generation)
        old_epoch = app._auth_epoch
        app.persist_account_session(account(NEW_COOKIE), generation=app.account_generation, login=True)
        self.run_job(1)  # newest save may finish before an older queued worker
        self.run_job(0)
        self.assertEqual(self.store.events, [("load",), ("save", NEW_COOKIE)])
        self.assertEqual(self.store.cookie, NEW_COOKIE)
        self.assertEqual(app.session["cookie"], NEW_COOKIE)
        self.assertNotEqual(app._auth_epoch, old_epoch)
        self.thread.reset_mock()
        app.show_new_token({"code": "OFFLINE-CODE-DO-NOT-PERSIST", "session_cookie": COOKIE})
        self.assertTrue(self.policy.value)
        self.run_job(0)  # obsolete delete must not erase a later token save
        self.run_job(1)
        self.assertEqual(self.store.cookie, COOKIE)
        self.assertFalse(self.policy.value)
        self.assertTrue(all("OFFLINE-CODE" not in repr(event) for event in self.store.events))
        self.assertEqual(app.hold.call_count, app.release.call_count)

    def test_real_blocking_save_then_logout_close_finishes_delete_when_marker_fails(self):
        app = self.app
        self.complete_empty_startup()
        started, release_save, deleted = threading.Event(), threading.Event(), threading.Event()
        workers = []
        def make_thread(**kwargs):
            worker = REAL_THREAD(**kwargs)
            workers.append(worker)
            return worker
        def blocked_save(cookie, cancel=None):
            self.store.events.append(("save", cookie))
            self.store.cookie = cookie
            started.set()
            if not release_save.wait(3):
                raise SessionStoreError("fixture release timeout")
        def delete(cancel=None):
            self.store.events.append(("delete",))
            self.assertFalse(cancel.is_cancelled())
            self.store.cookie = None
            deleted.set()
        self.store.save, self.store.delete = blocked_save, delete
        with patch("auth_persistence.threading.Thread", side_effect=make_thread):
            try:
                app.login_received(account(), "", app.account_generation)
                self.assertTrue(started.wait(2))
                with patch.object(self.policy, "disable", side_effect=OSError("fixture read-only marker")):
                    app.finish_account_logout()
                delete_epoch = app._auth_epoch
                self.assertEqual(app._auth_operation, "delete")
                app.close_window(app.window)
                self.assertEqual(app._auth_epoch, delete_epoch)
                release_save.set()
                self.assertTrue(deleted.wait(2))
            finally:
                release_save.set()
                for worker in workers:
                    worker.join(timeout=3)
        context = GLib.MainContext.default()
        while context.pending():
            context.iteration(False)
        self.assertIsNone(self.store.cookie)
        self.assertIsNone(app.session)
        self.assertNotIn("enable", self.policy.events)
        self.assertEqual(self.store.events[-2:], [("save", COOKIE), ("delete",)])
        self.assertEqual(app.hold.call_count, app.release.call_count)

    def test_close_during_open_keyring_save_prompt_finishes_without_restoring_ui(self):
        app = self.app
        self.complete_empty_startup()
        app.persist_account_session(account(), generation=app.account_generation, login=True)
        epoch = app._auth_epoch
        self.store.during_save = lambda: app.close_window(app.window)
        self.run_job(0)
        self.assertTrue(app.closing)
        self.assertEqual(app._auth_epoch, epoch)
        self.assertEqual(self.store.cookie, COOKIE)
        self.assertFalse(self.policy.value)
        self.assertIsNone(app.session)
        self.assertEqual(app.hold.call_count, app.release.call_count)

    def test_close_keeps_confirmed_save_alive_but_cancels_old_restore(self):
        app = self.app
        old_epoch = app._auth_epoch
        app.close_window(app.window)
        self.run_job(0)
        self.assertNotEqual(app._auth_epoch, old_epoch)
        self.assertEqual(self.store.events, [])
        self.assertIsNone(app.session)
        app.closing = False
        app.persist_account_session(account(), generation=app.account_generation, login=True)
        save_epoch = app._auth_epoch
        app.close_window(app.window)
        self.assertEqual(app._auth_epoch, save_epoch)
        self.run_job(1)
        self.assertEqual(self.store.cookie, COOKIE)
        self.assertIsNone(app.session)  # no closed-window UI mutation
        self.assertEqual(app.hold.call_count, app.release.call_count)


class RealApplicationCloseTests(unittest.TestCase):
    def test_application_stays_alive_until_save_finishes_after_last_window_closes(self):
        close_seen, completed = threading.Event(), threading.Event()
        state = {"timed_out": False}
        store, policy = MemoryStore(), MemoryPolicy(disabled=True)
        original_save = store.save
        def save(cookie, cancel=None):
            if not close_seen.wait(2):
                raise SessionStoreError("fixture last window did not close")
            original_save(cookie, cancel)
            completed.set()
        store.save = save
        class LifecycleApp(Quantway):
            def __init__(self):
                super().__init__()
                self.set_application_id("link.quantway.authfixture.lastwindow")
                self.set_flags(Gio.ApplicationFlags.NON_UNIQUE)
                self.session_store, self.restore_policy = store, policy
            def do_activate(self):
                super().do_activate()
                GLib.idle_add(self.begin_close)
            def begin_close(self):
                self.login_received(account(), "", self.account_generation)
                self.window.close()
                close_seen.set()
                return False
            def call_helper(self, *_args, **_kwargs):
                raise AssertionError("Real helper forbidden")
            def refresh_account_stats(self):
                pass
        with patch("session_store.Gio.bus_get_sync", side_effect=AssertionError("Host DBus forbidden")):
            app = LifecycleApp()
            def timeout():
                state["timed_out"] = True
                app.quit()
                return False
            watchdog = GLib.timeout_add(4000, timeout)
            try:
                self.assertEqual(app.run([]), 0)
            finally:
                if not state["timed_out"]:
                    GLib.source_remove(watchdog)
                app.stop_support_poll()
                app.window.destroy()
        self.assertFalse(state["timed_out"])
        self.assertTrue(completed.is_set())
        self.assertTrue(app.closing)
        self.assertEqual(store.cookie, COOKIE)
        self.assertFalse(policy.disabled())
        self.assertIsNone(app.session)


if __name__ == "__main__":
    unittest.main(verbosity=2)
