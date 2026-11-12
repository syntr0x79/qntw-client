#!/usr/bin/env python3
"""Offline GTK route browsing/filtering; no helper or network operation."""
import unittest
import uuid
import io
from unittest.mock import patch

import gi
gi.require_version("Gtk", "4.0")
from gi.repository import Gio, Gtk
from quantway import Quantway
from test_auth_helpers import block_auth_io


class RoutePresentationTests(unittest.TestCase):
    def setUp(self):
        self.auth = block_auth_io(Quantway)
        self.background = patch("quantway.threading.Thread")
        self.thread = self.background.start()
        self.stats = patch.object(Quantway, "refresh_account_stats")
        self.stats.start()
        self.app = Quantway()
        self.app.set_application_id("link.quantway.test.routes.c" + uuid.uuid4().hex)
        self.app.set_flags(Gio.ApplicationFlags.NON_UNIQUE)
        self.assertTrue(self.app.register(None))
        self.app.activate()
        self.app.entry.set_text("https://example.invalid/ui-route-fixture")
        self.app.loaded_link = self.app.entry.get_text()
        self.app.results_stale = False
        self.profiles = [
            {"job_id": "vless", "label": "Берлин", "protocol": "VLESS", "transport": "XHTTP", "outcome": "ok", "elapsed_ms": 22, "job": {"engine": "xray", "credential": "not-searchable-private-value"}},
            {"job_id": "awg", "label": "Амстердам", "protocol": "AWG", "outcome": "ok", "elapsed_ms": 30, "job": {"engine": "amneziawg"}},
            {"job_id": "failed", "label": "Недоступный", "protocol": "VLESS", "outcome": "fail"},
        ]
        self.app.show_result(self.profiles, "")
        self.app.selected_profile = self.profiles[0]
        self.app.restore_selected_row()
        self.thread.reset_mock()

    def tearDown(self):
        self.app.window.destroy()
        self.app.quit()
        self.stats.stop()
        self.background.stop()
        self.auth.stop()

    def test_idle_choose_and_apply_only_navigates_home(self):
        app = self.app
        app.listbox.select_row(app.listbox.get_row_at_index(1))
        self.assertEqual(app.selected_profile["job_id"], "awg")
        self.assertFalse(app.selection_automatic)
        self.assertEqual(app.route_apply_button.get_label(), "На Главную")
        app.apply_route_preview()
        self.assertEqual(app.stack.get_visible_child_name(), "home")
        self.assertIsNone(app.vpn_process)
        self.thread.assert_not_called()

    def test_search_is_public_label_technology_only(self):
        from route_ui import matches_filter
        app = self.app
        for text, expected in [("берЛИН", [True, False, False]), ("xhttp", [True, False, False]), ("awg", [False, True, False]), ("not-searchable-private-value", [False, False, False])]:
            app.route_search.set_text(text)
            app.filter_routes()
            self.assertEqual([matches_filter(app, p) for p in self.profiles], expected)
        self.assertTrue(app.routes_empty.get_visible())
        self.assertIn("Ничего не найдено", app.routes_empty.get_text())
        self.assertFalse(app.route_apply_button.get_sensitive())
        self.assertEqual(app.selected_profile["job_id"], "vless")

    def test_available_filter_never_turns_failed_job_into_action(self):
        from route_ui import matches_filter
        app = self.app
        app.route_available.set_active(True)
        app.filter_routes()
        self.assertEqual([matches_filter(app, p) for p in self.profiles], [True, True, False])
        app.choose_best(None, self.profiles[2])
        self.assertEqual(app.selected_profile["job_id"], "vless")
        self.thread.assert_not_called()
        app.update_route_card()
        self.assertEqual(app.route_detail.get_text(), "Проверка: 22 мс")
        from route_ui import route_measurement
        self.assertEqual(route_measurement(self.profiles[2]), "Не прошёл проверку")
        self.assertEqual(route_measurement({}), "Не проверен")

    def test_tinc_and_cleanup_ownership_block_route_apply(self):
        app = self.app
        for flag, value in [("tinc_worker_active", True), ("_vpn_worker_generation", 9), ("_device_mutating", True)]:
            old = getattr(app, flag)
            setattr(app, flag, value)
            app.choose_best(None, self.profiles[1])
            app.apply_route_preview()
            self.assertEqual(app.selected_profile["job_id"], "vless")
            self.assertIsNone(app.route_switch_dialog)
            setattr(app, flag, old)
        self.thread.assert_not_called()

    def test_tinc_does_not_claim_ordinary_route_connected_or_start_it(self):
        app = self.app
        app.connection_mode = "tinc"
        app.tinc_worker_active = True
        process = type("Process", (), {"stdin": io.StringIO()})()
        app.vpn_process = process
        app.nav_by_page["routes"].set_active(True)
        app.sync_controls()
        self.assertTrue(app.routes_lock_hint.get_visible())
        self.assertIn("Сейчас выбран Tinc", app.route_preview_detail.get_text())
        self.assertEqual(app.route_preview_title.get_text(), "Выбран Tinc")
        self.assertEqual(app.route_apply_button.get_label(), "На Главную")
        self.assertTrue(app.route_apply_button.get_sensitive())
        app.apply_route_preview()
        self.assertEqual(app.stack.get_visible_child_name(), "home")
        self.assertEqual(app.connection_mode, "tinc")
        self.assertEqual(app.selected_profile["job_id"], "vless")
        self.assertEqual(process.stdin.getvalue(), "")
        self.thread.assert_not_called()


if __name__ == "__main__":
    unittest.main(verbosity=2)
