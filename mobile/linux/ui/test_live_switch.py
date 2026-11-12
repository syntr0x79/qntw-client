#!/usr/bin/env python3
"""Offline regression tests for switching an established VPN route.

Run under a Linux graphical session or Xvfb. Real subprocesses, helpers and
network requests are blocked; only production GTK callbacks are exercised.
"""
import io
import unittest
import uuid
from unittest.mock import patch

import gi
gi.require_version("Gtk", "4.0")
from gi.repository import Gio, GLib, Gtk
from quantway import Quantway
from test_auth_helpers import block_auth_io


class FakeProcess:
    def __init__(self):
        self.stdin = io.StringIO()


class BrokenInput:
    def write(self, _text):
        raise OSError("fixture stop failure")


class LiveRouteSwitchTests(unittest.TestCase):
    def setUp(self):
        self.background = patch("quantway.threading.Thread")
        self.thread = self.background.start()
        self.app = app = Quantway()
        app.set_application_id("link.quantway.test.routeswitch.c" + uuid.uuid4().hex)
        self.auth_guard = block_auth_io(app)
        self.stats_guard = patch.object(app, "refresh_account_stats")
        self.stats_guard.start()
        app.set_flags(Gio.ApplicationFlags.NON_UNIQUE)
        self.assertTrue(app.register(None))
        app.activate()
        self.helper = patch.object(app, "call_helper", side_effect=AssertionError("No helper may run in this test"))
        self.helper.start()
        self.link = "https://example.invalid/route-switch-fixture"
        app.entry.set_text(self.link)
        app.loaded_link = self.link
        self.profiles = [
            {"job_id": "one", "label": "Германия", "protocol": "VLESS", "outcome": "ok", "elapsed_ms": 20,
             "job": {"engine": "xray", "id": "tested-one"}, "control_url": "https://example.invalid/health"},
            {"job_id": "two", "label": "Нидерланды", "protocol": "AWG", "outcome": "ok", "elapsed_ms": 30,
             "job": {"engine": "amneziawg", "id": "tested-two"}, "control_url": "https://example.invalid/health"},
            {"job_id": "failed", "label": "Недоступный", "protocol": "VLESS", "outcome": "fail", "elapsed_ms": 0},
        ]
        app.results_stale = False
        app.show_result(self.profiles, "")
        app.selected_profile = self.profiles[0]
        app.restore_selected_row()
        self.process = FakeProcess()
        app.vpn_process = self.process
        app.vpn_generation = 7
        app.vpn_connected(7)
        self.thread.reset_mock()

    def tearDown(self):
        app = self.app
        if app.route_switch_dialog:
            app.route_switch_dialog.destroy()
            app.route_switch_dialog = None
        if app.connection_timer is not None:
            GLib.source_remove(app.connection_timer)
            app.connection_timer = None
        app.vpn_process = None
        app.window.destroy()
        app.quit()
        self.stats_guard.stop()
        self.helper.stop()
        self.auth_guard.stop()
        self.background.stop()

    def accept_dialog(self):
        self.assertIsNotNone(self.app.route_switch_dialog)
        self.app.route_switch_dialog.response(Gtk.ResponseType.ACCEPT)

    def test_cancel_keeps_connection_and_selected_profile(self):
        app = self.app
        app.choose_best(None, self.profiles[1])
        self.assertIsNone(app.route_switch_dialog)
        self.assertEqual(app.selected_profile["job_id"], "one")
        app.apply_route_preview()
        self.assertIsNotNone(app.route_switch_dialog)
        app.route_switch_dialog.response(Gtk.ResponseType.CANCEL)
        self.assertIsNone(app.route_switch_dialog)
        self.assertEqual(app.selected_profile["job_id"], "one")
        self.assertEqual(self.process.stdin.getvalue(), "")
        self.assertFalse(app.switching_route)
        self.assertIsNone(app.pending_route_switch)
        self.thread.assert_not_called()

    def test_confirm_waits_for_old_process_to_stop_before_starting_new(self):
        app = self.app
        app.choose_best(None, self.profiles[1])
        app.apply_route_preview()
        self.accept_dialog()
        self.assertEqual(self.process.stdin.getvalue(), "stop\n")
        self.assertEqual(app.selected_profile["job_id"], "one")
        self.assertEqual(app.pending_route_switch["job_id"], "two")
        self.assertTrue(app.switching_route)
        self.thread.assert_not_called()
        app.vpn_process = None  # The old worker reaped the process.
        app.vpn_stopped("", True, 7)
        self.assertEqual(app.selected_profile["job_id"], "two")
        self.assertEqual(app.vpn_generation, 8)
        self.assertTrue(app.vpn_starting)
        self.assertFalse(app.switching_route)
        self.assertIsNone(app.pending_route_switch)
        self.assertEqual(self.thread.call_args.kwargs["args"], (self.profiles[1], 8))
        self.thread.return_value.start.assert_called_once()

    def test_only_current_successfully_tested_job_can_be_accepted(self):
        app = self.app
        for profile in (self.profiles[2], {"job_id": "unknown", "label": "Несуществующий", "job": {"engine": "xray"}}):
            app.confirm_route_switch(profile)
            self.accept_dialog()
            self.assertIsNone(app.pending_route_switch)
            self.assertEqual(self.process.stdin.getvalue(), "")
        app.confirm_route_switch(dict(self.profiles[1], job={"engine": "untrusted-stale-job"}))
        self.accept_dialog()
        self.assertEqual(app.pending_route_switch["job"], self.profiles[1]["job"])

    def test_new_probe_is_blocked_but_route_selection_remains_available(self):
        app = self.app
        self.assertFalse(app.probe_button.get_sensitive())
        self.assertTrue(app.listbox.get_sensitive())
        self.assertTrue(app.best_list.get_first_child().get_sensitive())
        app.start_probe(None)
        self.assertFalse(app.probe_running)
        self.thread.assert_not_called()

    def test_old_generation_callbacks_cannot_replace_new_connection_state(self):
        app = self.app
        app.vpn_generation = 8
        app.vpn_starting = True
        app.connected_since = None
        before = app.status.get_text()
        app.vpn_connected(7)
        app.vpn_stopped("old worker error", True, 7)
        self.assertTrue(app.vpn_starting)
        self.assertIsNone(app.connected_since)
        self.assertEqual(app.status.get_text(), before)
        self.assertIs(app.vpn_process, self.process)
        self.thread.assert_not_called()

    def test_stop_failure_releases_switch_lock_and_keeps_old_route(self):
        app = self.app
        self.process.stdin = BrokenInput()
        app.choose_best(None, self.profiles[1])
        app.apply_route_preview()
        self.accept_dialog()
        self.assertFalse(app.switching_route)
        self.assertIsNone(app.pending_route_switch)
        self.assertEqual(app.selected_profile["job_id"], "one")
        self.assertTrue(app.connect_button.get_sensitive())
        self.assertTrue(app.listbox.get_sensitive())
        self.assertIn("Не удалось остановить", app.status.get_text())
        self.thread.assert_not_called()

    def test_best_and_route_list_preview_require_explicit_apply(self):
        app = self.app
        app.best_list.get_first_child().emit("clicked")
        self.assertIsNone(app.route_switch_dialog)
        self.assertEqual(app.selected_profile["job_id"], "one")
        self.assertIn("Нидерланды", app.route_preview_title.get_text())
        app.apply_route_preview()
        self.assertIsNotNone(app.route_switch_dialog)
        app.route_switch_dialog.response(Gtk.ResponseType.CANCEL)
        app.restore_selected_row()
        app.listbox.select_row(app.listbox.get_row_at_index(1))
        self.assertIsNone(app.route_switch_dialog)
        self.assertEqual(app.listbox.get_selected_row().profile["job_id"], "two")
        self.assertEqual(app.selected_profile["job_id"], "one")
        self.assertEqual(self.process.stdin.getvalue(), "")
        app.apply_route_preview()
        self.assertIsNotNone(app.route_switch_dialog)


if __name__ == "__main__":
    unittest.main(verbosity=2)
