#!/usr/bin/env python3
"""Real GTK callbacks with all credentials, network and helpers kept offline."""
import io
import json
import unittest
import uuid
from datetime import datetime, timezone
from unittest.mock import patch

import gi
gi.require_version("Gtk", "4.0")
from gi.repository import Gio, GLib, Gtk

from auth_persistence import ServiceFailure
from quantway import Quantway
from tinc_ui import TINC_FAILURE_MESSAGES
from test_auth_helpers import block_auth_io


class Process:
    def __init__(self, events=()):
        self.stdin = io.StringIO()
        self.stdout = io.StringIO("".join(json.dumps(item) + "\n" for item in events))
        self.returncode = 0

    def poll(self):
        return self.returncode

    def wait(self, timeout=None):
        return self.returncode


class TincTests(unittest.TestCase):
    def setUp(self):
        self.threads = patch("quantway.threading.Thread")
        self.thread = self.threads.start()
        self.app = app = Quantway()
        app.set_application_id("link.quantway.test.tinc.c" + uuid.uuid4().hex)
        self.auth = block_auth_io(app)
        self.stats = patch.object(app, "refresh_account_stats")
        self.stats.start()
        app.set_flags(Gio.ApplicationFlags.NON_UNIQUE)
        self.assertTrue(app.register(None))
        app.activate()
        self.helper = patch.object(app, "call_helper", side_effect=AssertionError("No real helper"))
        self.helper.start()
        self.link = "https://example.invalid/device/private-fixture"
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
        self.thread.reset_mock()

    def tearDown(self):
        app = self.app
        for dialog in (app.tinc_change_dialog, app.route_switch_dialog):
            if dialog:
                dialog.destroy()
        if app.connection_timer is not None:
            GLib.source_remove(app.connection_timer)
        if app.tinc_mesh_timer is not None:
            GLib.source_remove(app.tinc_mesh_timer)
        app.vpn_process = None
        app.window.destroy()
        app.quit()
        self.helper.stop()
        self.stats.stop()
        self.auth.stop()
        self.threads.stop()

    def catalog(self, codes=("PL", "SE"), requested="PL"):
        app = self.app
        app.open_tinc_page()
        context = app.tinc_source_context()
        app.tinc_catalog_received({"schema_version": 1, "state": "available", "requested_country": requested,
                                  "countries": [{"code": code, "state": "available"} for code in codes]},
                                 "", context, app.tinc_catalog_serial)
        self.thread.reset_mock()

    def active(self, state="connected"):
        self.catalog()
        app = self.app
        app.tinc_worker_active = True
        app.connection_mode = "tinc"
        app.vpn_process = Process()
        app.vpn_generation = 7
        app._vpn_worker_generation = 7
        app.tinc_vpn_event({"status": state, "country": "PL", "desired_country": "PL",
                            "connected_ingresses": ["one", "two"]}, 7, app.tinc_source_context())
        return app.vpn_process

    def test_only_current_quantway_device_shows_mode(self):
        app = self.app
        self.assertTrue(app.tinc_controls.get_visible())
        app.active_source = dict(app.active_source, kind="external")
        app.sync_controls()
        self.assertFalse(app.tinc_controls.get_visible())
        app.open_tinc_page()
        self.assertFalse(app.tinc_selected())
        self.thread.assert_not_called()
        app.active_source = dict(app.active_source, kind="quantway", id="another-device")
        app.sync_controls()
        self.assertFalse(app.tinc_controls.get_visible())

    def test_sidebar_tinc_is_directly_below_home_and_keeps_legacy_indices(self):
        app = self.app
        self.assertIs(app.nav_by_page["home"].get_next_sibling(), app.nav_by_page["tinc"])
        self.assertIs(app.nav_by_page["tinc"].get_next_sibling(), app.nav_by_page["routes"])
        for index, page in enumerate(("home", "routes", "account", "external")):
            self.assertIs(app.nav_buttons[index], app.nav_by_page[page])
        self.assertFalse(hasattr(app, "tinc_menu_button"))
        app.nav_by_page["tinc"].emit("clicked")
        self.assertTrue(app.nav_by_page["tinc"].get_active())
        self.assertFalse(app.nav_by_page["home"].get_active())
        self.assertEqual(app.stack.get_visible_child_name(), "tinc")

    def test_catalog_is_dynamic_and_preserves_requested_country(self):
        self.catalog(("NZ", "SE"), "NZ")
        app = self.app
        self.assertEqual([item["code"] for item in app.tinc_catalog], ["NZ", "SE"])
        self.assertEqual(app.tinc_country, "NZ")
        self.assertEqual(app.tinc_country_picker.get_model().get_string(0), "NZ")
        self.assertTrue(app.tinc_connect_button.get_sensitive())
        self.assertTrue(app.home_columns.get_visible())
        self.assertEqual(app.stack.get_visible_child_name(), "tinc")

    def test_catalog_403_is_plain_and_never_falls_back(self):
        app = self.app
        app.open_tinc_page()
        app.tinc_mode_button.set_active(True)
        context, serial = app.tinc_source_context(), app.tinc_catalog_serial
        callbacks = []
        with patch.object(app, "call_helper", side_effect=ServiceFailure("opaque", 403)), patch("tinc_ui.GLib.idle_add", side_effect=lambda fn, *args: callbacks.append((fn, args))):
            app.tinc_catalog_worker(context, serial)
        callbacks[0][0](*callbacks[0][1])
        self.assertEqual(app.connection_mode, "tinc")
        self.assertFalse(app.tinc_connect_button.get_sensitive())
        self.assertIn("Доступ к Tinc для этого устройства ограничен", app.notice_text.get_text())
        self.assertEqual(app.current_profiles, [self.profile])
        self.assertIsNotNone(app.session)

    def test_capacity_wait_explains_busy_and_keeps_stop_available(self):
        process = self.active("waiting")
        app = self.app
        app.tinc_vpn_event({"status": "waiting", "desired_country": "PL", "code": "capacity_exhausted", "retry_after": 3}, 7, app.tinc_source_context())
        self.assertIn("Все подключения заняты. Повторите позже.", app.notice_text.get_text())
        self.assertTrue(app.tinc_connect_button.get_sensitive())
        self.assertEqual(app.tinc_connect_button.get_label(), "Отключить")
        self.assertEqual(app.connection_mode, "tinc")
        app.toggle_tinc_page()
        self.assertEqual(process.stdin.getvalue(), "stop\n")
        self.thread.assert_not_called()

    def test_capacity_country_refusal_preserves_active_connection_then_clears(self):
        self.active()
        app = self.app
        since = app.connected_since
        context = app.tinc_source_context()
        app.tinc_vpn_event({"status": "connected", "country": "PL", "desired_country": "PL", "code": "capacity_exhausted"}, 7, context)
        self.assertEqual(app.tinc_country, "PL")
        self.assertEqual(app.connected_since, since)
        self.assertIn("Текущее соединение сохранено", app.notice_text.get_text())
        app.tinc_vpn_event({"status": "connected", "country": "PL", "desired_country": "PL"}, 7, context)
        self.assertNotIn("заняты", app.notice_text.get_text())
        self.thread.assert_not_called()

    def test_connected_time_survives_mesh_events_and_control_refreshes(self):
        with patch("quantway.time.monotonic", return_value=1000):
            self.active()
        app = self.app
        self.assertEqual(app.home_state.get_text(), "Подключён  00:00:00")
        timer = app.connection_timer
        self.assertIsNotNone(timer)
        labels = []
        app.home_state.connect("notify::label", lambda item, _p: labels.append(item.get_text()))
        for elapsed in (1, 2, 5, 65):
            expected = f"Подключён  00:{elapsed // 60:02d}:{elapsed % 60:02d}"
            with patch("quantway.time.monotonic", return_value=1000 + elapsed):
                app.update_connection_time()
                self.assertEqual(app.home_state.get_text(), expected)
                app.refresh_tinc_mesh()
                self.assertEqual(app.home_state.get_text(), expected)
                app.sync_controls()
                self.assertEqual(app.home_state.get_text(), expected)
                app.tinc_vpn_event({"status": "connected", "country": "PL"}, 7, app.tinc_source_context())
                self.assertEqual(app.home_state.get_text(), expected)
            self.assertEqual(app.connected_since, 1000)
            self.assertEqual(app.connection_timer, timer)
        self.assertTrue(labels)
        self.assertTrue(all(text.startswith("Подключён  00:") for text in labels), labels)
        self.thread.assert_not_called()

    def test_pending_country_change_timer_keeps_reconnecting_status(self):
        with patch("quantway.time.monotonic", return_value=1000):
            process = self.active()
        app = self.app
        app.tinc_country_picker.set_selected(1)
        app.tinc_change_dialog.response(Gtk.ResponseType.ACCEPT)
        self.assertEqual(json.loads(process.stdin.getvalue()), {"country": "SE"})
        state = app.home_state.get_text()
        self.assertEqual(state, "Переподключение…")
        with patch("quantway.time.monotonic", return_value=1005):
            self.assertTrue(app.update_connection_time())
        self.assertEqual(app.home_state.get_text(), state)
        self.assertEqual(app.connected_since, 1000)
        with patch("quantway.time.monotonic", return_value=1010):
            app.tinc_vpn_event({"status": "connected", "country": "PL", "desired_country": "PL",
                                "code": "capacity_exhausted"}, 7, app.tinc_source_context())
        self.assertEqual(app.home_state.get_text(), "Подключён  00:00:10")
        self.assertEqual(app.connected_since, 1000)
        self.thread.assert_not_called()

    def test_real_reconnection_resets_time_and_expiry_never_claims_connected(self):
        with patch("quantway.time.monotonic", return_value=1000):
            self.active()
        app = self.app
        context = app.tinc_source_context()
        app.tinc_vpn_event({"status": "reconnecting", "country": "PL"}, 7, context)
        self.assertIsNone(app.connected_since)
        self.assertEqual(app.home_state.get_text(), "Переподключение…")
        with patch("quantway.time.monotonic", return_value=1100):
            app.tinc_vpn_event({"status": "connected", "country": "PL"}, 7, context)
        self.assertEqual(app.connected_since, 1100)
        self.assertEqual(app.home_state.get_text(), "Подключён  00:00:00")
        app.tinc_vpn_event({"status": "expired", "country": "PL"}, 7, context)
        self.assertIsNone(app.connected_since)
        self.assertEqual(app.home_state.get_text(), "Доступ истёк")
        self.thread.assert_not_called()

    def test_expired_message_has_priority_over_capacity(self):
        self.active()
        app = self.app
        app.tinc_vpn_event({"status": "expired", "country": "PL", "code": "capacity_exhausted"}, 7, app.tinc_source_context())
        self.assertIsNone(app.connected_since)
        self.assertIn("Интернет заблокирован", app.notice_text.get_text())
        self.assertNotIn("Повторите позже", app.notice_text.get_text())
        self.assertTrue(app.tinc_connect_button.get_sensitive())
        self.thread.assert_not_called()

    def test_late_catalog_cannot_replace_another_account(self):
        app = self.app
        app.open_tinc_page()
        context, serial = app.tinc_source_context(), app.tinc_catalog_serial
        app.session = dict(app.session, cookie="different-account")
        app.tinc_catalog_received({"schema_version": 1, "state": "available", "countries": [{"code": "PL", "state": "available"}]}, "", context, serial)
        self.assertEqual(app.tinc_catalog, [])

    def test_active_country_change_requires_confirmation_and_uses_control(self):
        process = self.active()
        app = self.app
        app.tinc_country_picker.set_selected(1)
        self.assertIsNotNone(app.tinc_change_dialog)
        self.assertEqual(app.tinc_country, "PL")
        self.assertEqual(process.stdin.getvalue(), "")
        app.tinc_change_dialog.response(Gtk.ResponseType.CANCEL)
        self.assertEqual(app.tinc_country, "PL")
        app.tinc_country_picker.set_selected(1)
        app.tinc_change_dialog.response(Gtk.ResponseType.ACCEPT)
        self.assertEqual(json.loads(process.stdin.getvalue()), {"country": "SE"})
        self.assertEqual(app.tinc_country, "SE")
        self.assertFalse(app.tinc_country_picker.get_sensitive())
        self.thread.assert_not_called()

    def test_expired_retains_explicit_stop_and_does_not_claim_connected(self):
        process = self.active("expired")
        app = self.app
        self.assertIsNone(app.connected_since)
        self.assertEqual(app.connect_button.get_label(), "Отключить")
        self.assertTrue(app.connect_button.get_sensitive())
        self.assertIn("Интернет заблокирован", app.notice_text.get_text())
        app.toggle_vpn(None)
        self.assertEqual(process.stdin.getvalue(), "stop\n")
        self.assertEqual(app.connection_mode, "tinc")
        self.assertFalse(app.connect_button.get_sensitive())

    def test_waiting_can_be_stopped_and_locks_other_controls(self):
        process = self.active("waiting")
        app = self.app
        self.assertTrue(app.connect_button.get_sensitive())
        self.assertFalse(app.tinc_ordinary_button.get_sensitive())
        self.assertFalse(app.tinc_country_picker.get_sensitive())
        self.assertFalse(app.entry.get_sensitive())
        self.assertFalse(app.probe_button.get_sensitive())
        app.toggle_vpn(None)
        self.assertEqual(process.stdin.getvalue(), "stop\n")

    def test_mode_switch_waits_for_old_helper_cleanup(self):
        self.catalog()
        app = self.app
        process = Process()
        app.vpn_process = process
        app.vpn_generation = 7
        app._vpn_worker_generation = 7
        app.vpn_connected(7)
        app.toggle_tinc_page()
        self.assertEqual(app.connection_mode, "ordinary")
        app.tinc_change_dialog.response(Gtk.ResponseType.ACCEPT)
        self.assertEqual(process.stdin.getvalue(), "stop\n")
        self.thread.assert_not_called()
        app.vpn_process = None  # The worker has reaped the old helper.
        app.vpn_worker_finished("", True, 7)
        self.assertEqual(app.connection_mode, "tinc")
        self.assertTrue(app.tinc_worker_active)
        self.assertTrue(app.vpn_starting)
        self.assertEqual(app.vpn_generation, 8)
        self.assertEqual(self.thread.call_args.kwargs["target"], app.tinc_vpn_worker)

    def test_old_stream_event_cannot_change_current_account_or_generation(self):
        self.active()
        app = self.app
        before = dict(app.tinc_status)
        context = app.tinc_source_context()
        app.tinc_vpn_event({"status": "expired"}, 6, context)
        app.session = dict(app.session, cookie="another-account")
        app.tinc_vpn_event({"status": "expired"}, 7, context)
        self.assertEqual(app.tinc_status, before)

    def test_cleanup_error_blocks_automatic_mode_switch(self):
        self.active()
        app = self.app
        app.tinc_ordinary_requested()
        app.tinc_change_dialog.response(Gtk.ResponseType.ACCEPT)
        app.vpn_process = None
        app.vpn_worker_finished("Не удалось полностью восстановить сетевые настройки.", True, 7)
        self.assertEqual(app.connection_mode, "tinc")
        self.assertFalse(app.tinc_worker_active)
        self.assertFalse(app.switching_route)
        self.assertIsNone(app.pending_mode_change)
        self.assertIn("восстановить сетевые настройки", app.status.get_text())
        self.thread.assert_not_called()

    def test_return_to_ordinary_waits_for_tinc_cleanup_and_keeps_profile(self):
        process = self.active()
        app = self.app
        app.tinc_ordinary_requested()
        app.tinc_change_dialog.response(Gtk.ResponseType.ACCEPT)
        self.assertEqual(process.stdin.getvalue(), "stop\n")
        self.thread.assert_not_called()
        app.vpn_process = None
        app.vpn_worker_finished("", True, 7)
        self.assertEqual(app.connection_mode, "ordinary")
        self.assertEqual(app.selected_profile, self.profile)
        self.assertEqual(app.stack.get_visible_child_name(), "home")
        self.assertEqual(app.vpn_generation, 8)
        self.assertEqual(self.thread.call_args.kwargs["target"], app.vpn_worker)

    def test_lost_account_binding_allows_only_stop_of_old_tinc_session(self):
        process = self.active()
        app = self.app
        app.session = None
        app.sync_controls()
        self.assertFalse(app.tinc_country_picker.get_sensitive())
        self.assertFalse(app.tinc_ordinary_button.get_sensitive())
        self.assertTrue(app.tinc_connect_button.get_sensitive())
        app.confirm_tinc_change("mode", "ordinary")
        self.assertIsNone(app.tinc_change_dialog)
        app.toggle_tinc_page()
        self.assertEqual(process.stdin.getvalue(), "stop\n")
        self.thread.assert_not_called()

    def test_ordinary_terminal_cleanup_error_blocks_pending_tinc_start(self):
        app = self.app
        app.vpn_generation = 7
        app._vpn_worker_generation = 7
        app.pending_mode_change = ("tinc", app.tinc_source_context())
        app.switching_route = True
        process = Process([{"status": "connected"}, {"status": "failed", "error": "fixture cleanup failure"}])
        process.returncode = 1
        callbacks = []
        with patch("quantway.subprocess.Popen", return_value=process), patch("quantway.GLib.idle_add", side_effect=lambda fn, *args: callbacks.append((fn, args))):
            app.vpn_worker(self.profile, 7)
        for fn, args in callbacks:
            fn(*args)
        self.assertEqual(app.connection_mode, "ordinary")
        self.assertFalse(app.tinc_worker_active)
        self.assertFalse(app.switching_route)
        self.assertIsNone(app.pending_mode_change)
        self.assertIn("восстановить сетевые настройки", app.status.get_text())
        self.thread.assert_not_called()

    def test_worker_passes_private_link_only_through_stdin_and_streams_states(self):
        self.catalog()
        app = self.app
        app.toggle_tinc_page()
        process = Process([{"status": "waiting"}, {"status": "connected", "country": "PL"}, {"status": "expired"}])
        callbacks = []
        with patch("tinc_ui.subprocess.Popen", return_value=process) as popen, patch("tinc_ui.GLib.idle_add", side_effect=lambda fn, *args: callbacks.append((fn, args))):
            app.tinc_vpn_worker(app.tinc_source_context(), "PL", app.vpn_generation)
        self.assertEqual(popen.call_args.args[0][-1], "tinc-connect")
        self.assertNotIn(self.link, str(popen.call_args))
        self.assertEqual(json.loads(process.stdin.getvalue()), {"link": self.link, "country": "PL"})
        self.assertEqual([args[0]["status"] for fn, args in callbacks[:-1]], ["waiting", "connected", "expired"])
        self.assertIs(callbacks[-1][0].__func__, app.vpn_worker_finished.__func__)

    def test_known_helper_failure_reaches_notice_without_raw_error(self):
        for code in ("helper_missing", "helper_unsafe", "pkexec_missing", "authorization_failed", "dns_unavailable", "identity_conflict", "run_as_user"):
            self.catalog()
            app = self.app
            app.toggle_tinc_page()
            process = Process([{"status":"failed", "code":code, "error":"private-token-secret https://qntw.link/sub/secret"}])
            callbacks = []
            with patch("tinc_ui.subprocess.Popen", return_value=process), patch("tinc_ui.GLib.idle_add", side_effect=lambda fn, *args: callbacks.append((fn, args))):
                app.tinc_vpn_worker(app.tinc_source_context(), "PL", app.vpn_generation)
            for fn,args in callbacks:
                fn(*args)
            self.assertEqual(app.status.get_text(), TINC_FAILURE_MESSAGES[code])
            self.assertNotIn("private-token-secret", app.notice_text.get_text())
            self.assertFalse(app.tinc_worker_active)
            self.assertEqual(app.connection_mode,"tinc")

    def test_unknown_failure_text_is_never_shown(self):
        self.catalog()
        app=self.app
        app.toggle_tinc_page()
        process=Process([{"status":"failed","code":"private-token-secret","error":"private-token-secret"}])
        callbacks=[]
        with patch("tinc_ui.subprocess.Popen",return_value=process),patch("tinc_ui.GLib.idle_add",side_effect=lambda fn,*args:callbacks.append((fn,args))):
            app.tinc_vpn_worker(app.tinc_source_context(),"PL",app.vpn_generation)
        for fn,args in callbacks:fn(*args)
        self.assertNotIn("private-token-secret",app.status.get_text())

    def test_connection_controls_live_on_home_and_mesh_page_has_no_connect_button(self):
        self.catalog()
        app = self.app
        app.tinc_mode_button.set_active(True)
        app.open_connection_home()
        self.assertEqual(app.connection_mode, "tinc")
        self.assertEqual(app.stack.get_visible_child_name(), "home")
        self.assertTrue(app.tinc_country_field.get_visible())
        self.assertTrue(app.connect_button.is_ancestor(app.connection_panel))
        self.assertTrue(app.tinc_country_picker.is_ancestor(app.connection_panel))
        self.assertIs(app.tinc_connect_button, app.connect_button)
        app.open_tinc_page()
        self.assertFalse(app.connect_button.is_ancestor(app.stack.get_visible_child()))
        self.assertIn("не подключён", app.tinc_mesh_path.get_text())

    def test_explicit_mode_selection_preserves_ordinary_routes_and_cancel_restores_mode(self):
        self.active()
        app = self.app
        app.tinc_ordinary_button.set_active(True)
        self.assertIsNotNone(app.tinc_change_dialog)
        self.assertEqual(app.connection_mode, "tinc")
        self.assertTrue(app.tinc_mode_button.get_active())
        app.tinc_change_dialog.response(Gtk.ResponseType.CANCEL)
        self.assertTrue(app.tinc_mode_button.get_active())
        self.assertEqual(app.selected_profile, self.profile)

    def test_mesh_shows_real_snapshot_and_clears_after_stop(self):
        self.active()
        app = self.app
        now = datetime.now(timezone.utc).isoformat()
        app.tinc_vpn_event({"status": "connected", "country": "PL", "connected_ingresses": ["in-pl"], "mesh": {
            "observation_state": "ok", "observed_at": now,
            "ingresses": [{"name": "in-pl", "connected": True}, {"name": "in-us", "connected": False}],
            "egress": {"country": "PL"}, "traffic": {"source": "system_tun", "epoch": "one", "sampled_at": now,
                                                       "rx_bytes": 4096, "tx_bytes": 1024}}}, 7, app.tinc_source_context())
        self.assertEqual(app.tinc_mesh_header.get_text(), "Входы: 1 из 2")
        self.assertEqual(app._tinc_node_widgets["in-pl"].get_text(), "Подключён")
        self.assertEqual(app._tinc_node_widgets["in-us"].get_text(), "Нет соединения")
        self.assertIn("Польша", app.tinc_mesh_path.get_text())
        self.assertIn("4,0 КиБ", app.tinc_received.get_text())
        app.vpn_process = None
        app.vpn_worker_finished("", True, 7)
        self.assertEqual(app.tinc_mesh_nodes.get_first_child(), None)
        self.assertEqual(app.tinc_received.get_text(), "Получено: —")
        self.assertEqual(app.connect_button.get_label(), "Подключить")


if __name__ == "__main__":
    unittest.main(verbosity=2)
