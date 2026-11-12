#!/usr/bin/env python3
"""Source invalidation regression tests; run in a GTK session or Xvfb.

All worker threads and helper processes are mocked. The tests drive actual UI
callbacks and deliberately deliver results after their subscription was removed.
"""
import io
import unittest
from unittest.mock import patch

from gi.repository import Gio, GLib
from quantway import Quantway
from test_auth_helpers import block_auth_io


class FakeProcess:
    def __init__(self):
        self.stdin = io.StringIO()
        self.stdout = io.StringIO()
        self.returncode = None
        self.reaped = False
        self.terminated = False

    def poll(self):
        return self.returncode

    def terminate(self):
        self.terminated = True
        self.returncode = -15

    def kill(self):
        self.returncode = -9

    def wait(self, timeout=None):
        self.reaped = True
        if self.returncode is None:
            self.returncode = 0
        return self.returncode


class SourceLifecycleTests(unittest.TestCase):
    def setUp(self):
        self.threads = patch("quantway.threading.Thread")
        self.thread = self.threads.start()
        self.app = app = Quantway()
        self.auth_guard = block_auth_io(app)
        self.stats_guard = patch.object(app, "refresh_account_stats")
        self.stats_guard.start()
        app.set_flags(Gio.ApplicationFlags.NON_UNIQUE)
        self.assertTrue(app.register(None))
        app.activate()
        self.helper = patch.object(app, "call_helper", side_effect=AssertionError("Real helpers are forbidden"))
        self.helper.start()
        self.old_link = "https://example.invalid/old-source"
        self.new_link = "https://example.invalid/new-source"
        app.active_source = {"kind": "quantway", "id": "old-device", "name": "Quantway", "link": self.old_link}
        app.entry.set_text(self.old_link)
        app.loaded_link = self.old_link
        self.profile = {"job_id": "old-job", "label": "Старый маршрут", "outcome": "ok", "elapsed_ms": 20,
                        "job": {"engine": "xray"}, "control_url": "https://example.invalid/health"}
        app.results_stale = False
        app.show_result([self.profile], "")
        app.selected_profile = self.profile
        app.sync_controls()
        self.thread.reset_mock()

    def tearDown(self):
        app = self.app
        if app.connection_timer is not None:
            GLib.source_remove(app.connection_timer)
        app.probe_process = None
        app.vpn_process = None
        app.window.destroy()
        app.quit()
        self.stats_guard.stop()
        self.helper.stop()
        self.auth_guard.stop()
        self.threads.stop()

    def test_late_fetch_cannot_restore_removed_source_or_clear_new_fetch(self):
        app = self.app
        app.load(None)
        old_generation = app.subscription_generation
        self.assertTrue(app.loading_profiles)
        app.clear_subscription_source("quantway", "old-device")
        self.assertFalse(app.loading_profiles)
        app.select_subscription(self.new_link, "external", "new-source", "Рабочий VPN")
        new_generation = app.subscription_generation
        self.assertTrue(app.loading_profiles)
        status = app.status.get_text()
        app.show_result([self.profile], "", self.old_link, old_generation)
        app.show_result([], "Запоздалая ошибка старого источника", self.old_link, old_generation)
        self.assertTrue(app.loading_profiles)
        self.assertEqual(app.entry.get_text(), self.new_link)
        self.assertEqual(app.active_source["id"], "new-source")
        self.assertEqual(app.current_profiles, [])
        self.assertEqual(app.status.get_text(), status)
        self.assertFalse(app.probe_running)
        new_profile = dict(self.profile, job_id="new-job", label="Новый маршрут")
        app.show_result([new_profile], "", self.new_link, new_generation)
        self.assertEqual(app.loaded_link, self.new_link)
        self.assertEqual(app.current_profiles[0]["job_id"], "new-job")
        self.assertTrue(app.probe_running)
        self.assertEqual(self.thread.call_args.kwargs["args"][0], self.new_link)

    def test_cancelled_probe_owns_busy_slot_until_its_helper_is_reaped(self):
        app = self.app
        app.start_probe(None)
        generation, cancel = app._probe_generation, app._probe_cancel
        process = FakeProcess()
        callbacks = []
        def launch(*_args, **_kwargs):
            # Invalidate while Popen is starting, before the process reference
            # is available to the UI cancellation path.
            app.clear_subscription_source("quantway", "old-device")
            self.assertTrue(app.probe_running)
            self.assertTrue(cancel.is_set())
            self.assertFalse(app.select_subscription(self.new_link, "external", "new-source", "Другой VPN"))
            return process
        with patch("quantway.subprocess.Popen", side_effect=launch), patch("quantway.GLib.idle_add", side_effect=lambda fn, *args: callbacks.append((fn, args))):
            app.probe_worker(self.old_link, generation, cancel)
        self.assertTrue(process.reaped)
        self.assertTrue(process.terminated)
        self.assertEqual(process.stdin.getvalue(), "")
        self.assertIsNone(app.probe_process)
        self.assertTrue(app.probe_running)
        self.assertEqual(len(callbacks), 1)
        fn, args = callbacks[0]
        fn(*args)
        self.assertFalse(app.probe_running)
        self.assertIsNone(app._probe_generation)
        self.assertEqual(app.current_profiles, [])
        self.assertEqual(app.loaded_link, "")
        self.assertFalse(app.connect_button.get_sensitive())

    def test_old_probe_progress_and_completion_leave_new_probe_untouched(self):
        app = self.app
        app.subscription_generation = 8
        app._probe_generation = 8
        app.probe_running = True
        app.show_progress(2, 10, 8)
        before = app.status.get_text()
        app.show_progress(10, 10, 7)
        app.show_probe([dict(self.profile, label="Нельзя восстановить")], "", 7)
        self.assertTrue(app.probe_running)
        self.assertEqual(app._probe_generation, 8)
        self.assertEqual(app.progress_percent.get_text(), "20 %")
        self.assertEqual(app.status.get_text(), before)
        self.assertEqual(app.current_profiles[0]["label"], "Старый маршрут")

    def test_deferred_import_starts_only_after_cancelled_probe_releases_slot(self):
        app = self.app
        app.start_probe(None)
        old_generation = app._probe_generation
        app.clear_subscription_source("quantway", "old-device")
        app.session = {"cookie": "qw_session=new-account", "current_device": "new-device"}
        app.queue_account_subscription(self.new_link)
        self.assertIsNotNone(app.pending_account_subscription)
        self.assertEqual(app.entry.get_text(), "")
        app.show_probe([self.profile], "", old_generation)
        self.assertFalse(app.probe_running)
        self.assertTrue(app.loading_profiles)
        self.assertIsNone(app.pending_account_subscription)
        self.assertEqual(app.entry.get_text(), self.new_link)
        self.assertEqual(app.active_source["id"], "new-device")

    def test_invalidated_vpn_worker_never_launches_its_old_job(self):
        app = self.app
        app.toggle_vpn(None)
        generation = app.vpn_generation
        self.assertTrue(app.vpn_starting)
        app.clear_subscription_source("quantway", "old-device")
        callbacks = []
        with patch("quantway.subprocess.Popen") as popen, patch("quantway.GLib.idle_add", side_effect=lambda fn, *args: callbacks.append((fn, args))):
            app.vpn_worker(self.profile, generation)
        popen.assert_not_called()
        self.assertTrue(app.vpn_starting)
        self.assertEqual(len(callbacks), 1)
        fn, args = callbacks[0]
        fn(*args)
        self.assertFalse(app.vpn_starting)
        self.assertIsNone(app._vpn_worker_generation)
        self.assertIsNone(app.vpn_process)
        self.assertFalse(app.connect_button.get_sensitive())

    def test_invalidation_during_vpn_launch_sends_no_job_and_waits_for_cleanup(self):
        app = self.app
        app.toggle_vpn(None)
        generation = app.vpn_generation
        process = FakeProcess()
        callbacks = []
        def launch(*_args, **_kwargs):
            app.clear_subscription_source("quantway", "old-device")
            self.assertTrue(app.vpn_starting)
            return process
        with patch("quantway.subprocess.Popen", side_effect=launch), patch("quantway.GLib.idle_add", side_effect=lambda fn, *args: callbacks.append((fn, args))):
            app.vpn_worker(self.profile, generation)
        self.assertEqual(process.stdin.getvalue(), "")
        self.assertTrue(process.reaped)
        self.assertTrue(process.terminated)
        self.assertTrue(app.vpn_starting)
        fn, args = callbacks[0]
        fn(*args)
        self.assertFalse(app.vpn_starting)
        self.assertEqual(app.current_profiles, [])

    def test_stale_vpn_finished_cannot_release_new_workers_slot(self):
        app = self.app
        app.vpn_generation = 11
        app._vpn_worker_generation = 11
        app.vpn_starting = True
        current = FakeProcess()
        app.vpn_process = current
        app.vpn_worker_finished("old failure", False, 10)
        self.assertTrue(app.vpn_starting)
        self.assertIs(app.vpn_process, current)
        self.assertEqual(app._vpn_worker_generation, 11)

    def test_device_mutation_blocks_new_transport_operations(self):
        app = self.app
        app._device_mutating = True
        app.sync_controls()
        self.assertFalse(app.connect_button.get_sensitive())
        self.assertFalse(app.probe_button.get_sensitive())
        self.assertFalse(app.listbox.get_sensitive())
        app.load(None)
        app.start_probe(None)
        app.toggle_vpn(None)
        self.assertFalse(app.select_subscription(self.new_link, "external", "new-source", "Другой VPN"))
        self.thread.assert_not_called()
        self.assertEqual(app.entry.get_text(), self.old_link)

    def test_removing_an_unrelated_source_does_not_invalidate_current_work(self):
        app = self.app
        generation = app.subscription_generation
        app.clear_subscription_source("quantway", "unrelated-device")
        self.assertEqual(app.subscription_generation, generation)
        self.assertEqual(app.loaded_link, self.old_link)
        self.assertEqual(app.selected_profile, self.profile)


if __name__ == "__main__":
    unittest.main(verbosity=2)
