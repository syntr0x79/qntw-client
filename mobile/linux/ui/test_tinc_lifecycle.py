#!/usr/bin/env python3
"""Shared Home action lifecycle; all network, helpers and threads stay offline."""
import io
import unittest
import uuid
from unittest.mock import patch

import gi
gi.require_version("Gtk", "4.0")
from gi.repository import Gio, GLib

from quantway import Quantway
from test_auth_helpers import block_auth_io


class Process:
    def __init__(self):
        self.stdin = io.StringIO()


class TincLifecycleTests(unittest.TestCase):
    def setUp(self):
        self.threads = patch("quantway.threading.Thread")
        self.thread = self.threads.start()
        self.app = app = Quantway()
        app.set_application_id("link.quantway.test.lifecycle.c" + uuid.uuid4().hex)
        self.auth = block_auth_io(app)
        self.stats = patch.object(app, "refresh_account_stats")
        self.stats.start()
        app.set_flags(Gio.ApplicationFlags.NON_UNIQUE)
        self.assertTrue(app.register(None))
        app.activate()
        self.helper = patch.object(app, "call_helper", side_effect=AssertionError("No real helper"))
        self.helper.start()
        self.link = "https://example.invalid/lifecycle-fixture"
        app.session = {"cookie": "fixture-session", "current_device": "self", "device_link": self.link}
        app.active_source = {"kind": "quantway", "id": "self", "name": "Quantway", "link": self.link}
        app.entry.set_text(self.link)
        app.loaded_link = self.link
        app.results_stale = False
        self.profile = {"job_id": "one", "label": "Обычный маршрут", "outcome": "ok", "elapsed_ms": 20,
                        "job": {"engine": "xray"}, "control_url": "https://example.invalid/health"}
        app.current_profiles = [self.profile]
        app.selected_profile = self.profile
        app.sync_controls()
        app.tinc_catalog_context = app.tinc_source_context()
        app.tinc_catalog = [{"code": "PL", "state": "available"}]
        app.tinc_catalog_state = "available"
        app.tinc_country = "PL"
        app.connection_mode = "tinc"
        app.render_tinc_choices()
        app.sync_controls()
        self.thread.reset_mock()

    def tearDown(self):
        app = self.app
        for dialog in (app.tinc_change_dialog, app.route_switch_dialog):
            if dialog:
                dialog.destroy()
        if app.connection_timer is not None:
            GLib.source_remove(app.connection_timer)
            app.connection_timer = None
        app.vpn_process = None
        app.window.destroy()
        app.quit()
        self.helper.stop()
        self.stats.stop()
        self.auth.stop()
        self.threads.stop()

    def active_tinc(self):
        app = self.app
        app.tinc_worker_active = True
        app.vpn_process = Process()
        app.vpn_generation = app._vpn_worker_generation = 7
        app.tinc_vpn_event({"status": "connected", "country": "PL", "connected_ingresses": ["one"]},
                           7, app.tinc_source_context())
        return app.vpn_process

    def test_stop_then_home_connect_retains_tinc_and_never_starts_ordinary(self):
        process = self.active_tinc()
        app = self.app
        self.assertEqual(app.connect_button.get_label(), "Отключить")
        app.toggle_vpn(None)
        self.assertEqual(process.stdin.getvalue(), "stop\n")
        app.vpn_process = None  # Worker has reaped the helper before completion.
        app.vpn_worker_finished("", True, 7)
        self.assertEqual(app.connection_mode, "tinc")
        self.assertEqual(app.connect_button.get_label(), "Подключить")
        app.toggle_vpn(None)
        self.assertEqual(self.thread.call_args.kwargs["target"], app.tinc_vpn_worker)
        self.assertEqual(app.connection_mode, "tinc")

    def test_idle_selected_tinc_home_action_does_not_start_ordinary(self):
        self.app.toggle_vpn(None)
        self.assertEqual(self.thread.call_args.kwargs["target"], self.app.tinc_vpn_worker)

    def test_invalid_tinc_binding_never_falls_back_to_ordinary(self):
        app = self.app
        app.session = None
        app.toggle_vpn(None)
        self.thread.assert_not_called()
        self.assertEqual(app.connection_mode, "tinc")

    def test_running_ordinary_helper_owns_stop_even_if_selected_mode_is_tinc(self):
        app = self.app
        process = app.vpn_process = Process()
        with patch.object(app, "toggle_tinc_vpn") as tinc:
            app.toggle_vpn(None)
        tinc.assert_not_called()
        self.assertEqual(process.stdin.getvalue(), "stop\n")
        self.thread.assert_not_called()

    def test_running_tinc_still_stops_after_account_binding_disappears(self):
        process = self.active_tinc()
        self.app.session = None
        self.app.toggle_vpn(None)
        self.assertEqual(process.stdin.getvalue(), "stop\n")
        self.thread.assert_not_called()

    def test_reaped_tinc_worker_cannot_restart_until_completion_releases_slot(self):
        self.active_tinc()
        app = self.app
        app.vpn_process = None
        app.sync_controls()
        self.assertFalse(app.connect_button.get_sensitive())
        app.toggle_vpn(None)
        self.thread.assert_not_called()
        self.assertEqual(app._vpn_worker_generation, 7)

    def test_stale_completion_cannot_release_newer_worker_or_reset_its_label(self):
        process = self.active_tinc()
        app = self.app
        app.vpn_worker_finished("old failure", True, 6)
        self.assertIs(app.vpn_process, process)
        self.assertEqual(app._vpn_worker_generation, 7)
        self.assertEqual(app.connect_button.get_label(), "Отключить")

    def test_invalidated_completion_preserves_cleanup_error_and_cancels_switch(self):
        self.active_tinc()
        app = self.app
        app.pending_mode_change = ("ordinary", app.tinc_source_context())
        app.switching_route = True
        app.vpn_generation = 8
        app.vpn_process = None
        app.vpn_worker_finished("Не удалось восстановить сетевые настройки.", True, 7)
        self.assertIn("восстановить сетевые настройки", app.status.get_text())
        self.assertEqual(app.connection_mode, "tinc")
        self.assertEqual(app.connect_button.get_label(), "Подключить")
        self.assertIsNone(app.pending_mode_change)
        self.thread.assert_not_called()

    def test_ordinary_route_cleanup_failure_never_auto_starts_pending_route(self):
        app = self.app
        app.connection_mode = "ordinary"
        app.pending_route_switch = dict(self.profile)
        app.switching_route = True
        app.vpn_stopped("Ошибка восстановления сети.", True)
        self.thread.assert_not_called()
        self.assertIsNone(app.pending_route_switch)
        self.assertEqual(app.connect_button.get_label(), "Подключить")
        self.assertEqual(app.status.get_text(), "Ошибка восстановления сети.")

    def test_closing_ignores_home_action(self):
        self.app.closing = True
        self.app.toggle_vpn(None)
        self.thread.assert_not_called()


if __name__ == "__main__":
    unittest.main()
