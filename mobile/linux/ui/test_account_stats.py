#!/usr/bin/env python3
"""Offline GTK checks for account usage values and asynchronous refreshes."""
import unittest
from unittest.mock import patch

import gi
gi.require_version("Gtk", "4.0")
from gi.repository import Gio

from quantway import Quantway
from test_auth_helpers import block_auth_io

SELF = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
OTHER = "11111111-2222-4333-8444-555555555555"


def account(cookie="a", current=SELF, role="retail"):
    return {"cookie": "qw_session=stats-account-" + cookie, "identity": {"kind": role}, "current_device": current,
            "account": {"subscription": {"headline": "Quantway", "status": "active", "days_left": 24, "devices": 7}}}


def snapshot(month=4096, own=2048, current=SELF):
    other = SELF if current == OTHER else OTHER
    return {"month_bytes": month, "in_use": 3, "paid": 7, "online_count": 1,
            "devices": [{"id": other, "month_bytes": 8192}, {"id": current, "month_bytes": own}]}


class AccountStatisticsTests(unittest.TestCase):
    def setUp(self):
        self.background = patch("quantway.threading.Thread")
        self.thread = self.background.start()
        self.app = app = Quantway()
        self.auth_guard = block_auth_io(app)
        app.set_flags(Gio.ApplicationFlags.NON_UNIQUE)
        self.assertTrue(app.register(None))
        app.activate()
        self.helper = patch.object(app, "call_helper", side_effect=AssertionError("No real helper requests are allowed"))
        self.helper.start()
        app.session = account()
        app.render_account(app.session)
        app.reset_account_stats()
        self.thread.reset_mock()

    def tearDown(self):
        self.app.closing = True
        self.app.stop_support_poll()
        self.app.window.destroy()
        self.app.quit()
        self.helper.stop()
        self.auth_guard.stop()
        self.background.stop()

    def values(self):
        return {key: label.get_text() for key, label in self.app.account_stat_values.items()}

    def test_missing_data_is_not_presented_as_zero(self):
        app = self.app
        self.assertEqual(set(self.values().values()), {"—"})
        app.refresh_account_stats()
        self.assertTrue(app._account_stats_loading)
        self.assertEqual(set(self.values().values()), {"—"})
        app.account_stats_received(app.account_context(), app._account_stats_serial, {})
        self.assertFalse(app._account_stats_loading)
        self.assertEqual(set(self.values().values()), {"—"})

    def test_real_zero_and_authoritative_slot_counts_are_displayed(self):
        app = self.app
        app.update_account_stats({"month_bytes": 0, "in_use": 0, "paid": 7, "online_count": 0,
                                  "devices": [{"id": SELF, "month_bytes": 0}]})
        self.assertEqual(self.values(), {"month": "0 Б", "self_month": "0 Б", "places": "0 из 7", "online": "0"})

    def test_self_traffic_uses_bound_account_device_not_external_source(self):
        app = self.app
        app.active_source = {"kind": "external", "id": OTHER, "name": "Другой VPN", "link": "https://example.invalid/sub"}
        app.update_account_stats(snapshot())
        self.assertEqual(self.values(), {"month": "4.0 КБ", "self_month": "2.0 КБ", "places": "3 из 7", "online": "1"})
        app.update_account_stats({"month_bytes": 4096, "paid": 7, "devices": [{"id": OTHER, "month_bytes": 8192}]})
        self.assertEqual(self.values(), {"month": "4.0 КБ", "self_month": "—", "places": "—", "online": "—"})
        app.update_account_stats({"month_bytes": -1, "in_use": True, "paid": 7, "online_count": "0", "devices": [{"id": SELF}]})
        self.assertEqual(set(self.values().values()), {"—"})
        app.session["current_device"] = None
        app.update_account_stats({"devices": [{"month_bytes": 2048}]})
        self.assertEqual(self.values()["self_month"], "—")

    def test_worker_is_read_only_and_captures_dispatch_cookie(self):
        app = self.app
        app.refresh_account_stats()
        worker = self.thread.call_args.kwargs["target"]
        context, serial = app.account_context(), app._account_stats_serial
        app.session = account("b")
        app.account_generation += 1
        with patch.object(app, "call_helper", return_value={"data": snapshot()}) as helper, patch("account_stats.GLib.idle_add") as idle:
            worker()
            helper.assert_called_once_with("cabinet", {"cookie": "qw_session=stats-account-a", "method": "GET", "path": "devices"})
            self.assertEqual(idle.call_args.args[1:3], (context, serial))

    def test_old_account_response_cannot_overwrite_new_account(self):
        app = self.app
        app.refresh_account_stats()
        old_context, old_serial = app.account_context(), app._account_stats_serial
        app.show_devices(account("b", OTHER), "")
        new_context, new_serial = app.account_context(), app._account_stats_serial
        before = self.values()
        app.account_stats_received(old_context, old_serial, snapshot())
        self.assertEqual(self.values(), before)
        self.assertEqual(app.session["cookie"], "qw_session=stats-account-b")
        app.account_stats_received(new_context, new_serial, snapshot(month=8192, own=4096, current=OTHER))
        self.assertEqual(self.values()["month"], "8.0 КБ")
        self.assertEqual(self.values()["self_month"], "4.0 КБ")

    def test_out_of_order_refresh_and_device_push_do_not_restore_old_values(self):
        app = self.app
        app.refresh_account_stats()
        context, first = app.account_context(), app._account_stats_serial
        app.refresh_account_stats()
        second = app._account_stats_serial
        app.account_stats_received(context, second, snapshot(month=8192))
        latest = self.values()
        app.account_stats_received(context, first, snapshot(month=1024))
        app.account_stats_received(context, first, {}, True)
        self.assertEqual(self.values(), latest)
        self.assertFalse(app._account_stats_error)
        app.refresh_account_stats()
        pending = app._account_stats_serial
        app.update_account_stats(snapshot(month=16384, own=4096))
        app.account_stats_received(context, pending, snapshot(month=1024))
        self.assertEqual(self.values()["month"], "16.0 КБ")
        self.assertEqual(self.values()["self_month"], "4.0 КБ")

    def test_error_preserves_known_values_without_inventing_zero(self):
        app = self.app
        app.refresh_account_stats()
        app.account_stats_received(app.account_context(), app._account_stats_serial, {}, True)
        self.assertEqual(set(self.values().values()), {"—"})
        self.assertIn("недоступна", app.account_stats_hint.get_text())
        app.update_account_stats(snapshot())
        previous = self.values()
        app.refresh_account_stats()
        app.account_stats_received(app.account_context(), app._account_stats_serial, {}, True)
        self.assertEqual(self.values(), previous)
        self.assertIn("Последние данные", app.account_stats_hint.get_text())

    def test_no_session_and_owner_do_not_request_device_statistics(self):
        app = self.app
        app.session = None
        app.refresh_account_stats()
        self.thread.assert_not_called()
        app.session = account(role="owner")
        app.render_account(app.session)
        self.assertFalse(app.account_stats_panel.get_visible())
        self.thread.assert_not_called()


if __name__ == "__main__":
    unittest.main(verbosity=2)
