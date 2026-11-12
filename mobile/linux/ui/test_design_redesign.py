"""User-visible layout and message regressions with real GTK, no I/O."""
import io
import time
import unittest
import uuid
from unittest.mock import patch

import gi
gi.require_version("Gtk", "4.0")
from gi.repository import Gio, GLib, Gtk

from quantway import Quantway
from test_auth_helpers import block_auth_io


def settle():
    until = time.monotonic() + 0.25
    context = GLib.MainContext.default()
    while time.monotonic() < until:
        while context.pending():
            context.iteration(False)
        time.sleep(0.005)


class RedesignTests(unittest.TestCase):
    def setUp(self):
        self.threads = patch("quantway.threading.Thread")
        self.thread = self.threads.start()
        self.app = app = Quantway()
        app.set_application_id("link.quantway.test.redesign.c" + uuid.uuid4().hex)
        self.auth = block_auth_io(app)
        self.stats = patch.object(app, "refresh_account_stats")
        self.stats.start()
        app.set_flags(Gio.ApplicationFlags.NON_UNIQUE)
        self.assertTrue(app.register(None))
        self.font = Gtk.Settings.get_default().get_property("gtk-font-name")
        app.activate()
        self.helper = patch.object(app, "call_helper", side_effect=AssertionError("No helper I/O"))
        self.helper.start()
        self.link = "https://example.invalid/device/offline-redesign-fixture"
        app.session = {"cookie": "fixture", "current_device": "this", "device_link": self.link}
        app.active_source = {"kind": "quantway", "id": "this", "name": "Quantway", "link": self.link}
        app.loaded_link = self.link
        app.entry.set_text(self.link)
        app.sync_controls()

    def tearDown(self):
        app = self.app
        for name in ("connection_timer", "tinc_mesh_timer"):
            source = getattr(app, name, None)
            if source is not None:
                GLib.source_remove(source)
                setattr(app, name, None)
        for name in ("notice_dialog", "tinc_change_dialog", "route_switch_dialog"):
            dialog = getattr(app, name, None)
            if dialog is not None:
                dialog.destroy()
        app.vpn_process = None
        app.window.destroy()
        app.quit()
        Gtk.Settings.get_default().set_property("gtk-font-name", self.font)
        self.helper.stop()
        self.stats.stop()
        self.auth.stop()
        self.threads.stop()

    def test_routine_banner_is_quiet_but_full_error_has_keyboard_action(self):
        app = self.app
        app.status.set_text("")
        self.assertFalse(app.notice.get_visible())
        app.status.set_text("Проверка завершена. Работает 4 из 5 маршрутов.")
        self.assertFalse(app.notice.get_visible())
        message = "Не удалось обновить подключение. " + "Полное описание причины. " * 30
        app.status.set_text(message, kind="error")
        self.assertTrue(app.notice.get_visible())
        self.assertEqual(app.notice_text.get_accessible_role(), Gtk.AccessibleRole.ALERT)
        self.assertTrue(app.notice_details.get_focusable())
        app.notice_details.emit("clicked")
        self.assertEqual(app.notice_dialog.get_transient_for(), app.window)
        self.assertEqual(app._notice_full_text, message)
        app.notice_dialog.response(Gtk.ResponseType.CLOSE)

    def test_source_popup_never_exposes_private_subscription_links(self):
        app = self.app
        app.external_subscriptions = [{"id": "other", "name": "Рабочий VPN", "link": "https://example.invalid/private-secret-link"}]
        app.render_source_choices()
        def captions(widget):
            result = [widget.get_text()] if isinstance(widget, Gtk.Label) else []
            child = widget.get_first_child()
            while child:
                result.extend(captions(child))
                child = child.get_next_sibling()
            return result
        text = " ".join(captions(app.source_choices))
        self.assertIn("Рабочий VPN", text)
        self.assertNotIn("private-secret-link", text)
        self.assertNotIn(self.link, text)

    def test_catalog_error_survives_mode_choice_and_return_to_home(self):
        app = self.app
        message = "Доступ к Tinc для этого устройства ограничен. Обратитесь в поддержку."
        app.tinc_catalog_context = app.tinc_source_context()
        app.tinc_catalog_state = "unavailable"
        app.tinc_catalog_error = message
        app.apply_tinc_mode("tinc")
        self.assertFalse(app.connect_button.get_sensitive())
        self.assertIn(message, app.connection_hint.get_text())
        self.assertTrue(app.notice.get_visible())
        app.open_tinc_page()
        app.open_connection_home()
        self.assertTrue(app.notice.get_visible())
        self.assertEqual(app._notice_full_text, message)
        self.assertEqual(app.notice_text.get_accessible_role(), Gtk.AccessibleRole.ALERT)

    def test_connect_and_stop_stay_visible_with_large_text(self):
        app = self.app
        Gtk.Settings.get_default().set_property("gtk-font-name", "Sans 22")
        app.window.set_default_size(740, 620)
        context = app.tinc_source_context()
        app.tinc_catalog_received({"schema_version": 1, "state": "available", "requested_country": "PL",
            "countries": [{"code": "PL", "state": "available"}]}, "", context, app.tinc_catalog_serial)
        app.connection_mode = "tinc"
        for state in (None, "connected", "expired"):
            app.tinc_worker_active = state is not None
            app.vpn_process = type("Process", (), {"stdin": io.StringIO()})() if state else None
            app.tinc_status = {"status": state, "country": "PL"} if state else {}
            app.sync_controls()
            settle()
            ok, bounds = app.connect_button.compute_bounds(app.window)
            self.assertTrue(ok)
            self.assertTrue(app.connect_button.get_mapped())
            self.assertTrue(app.connect_button.get_sensitive())
            # GTK get_height() is the content box; the pointer target includes
            # CSS padding/border and is represented by these widget bounds.
            self.assertGreaterEqual(bounds.get_height(), 44)
            self.assertGreaterEqual(bounds.get_y(), 0)
            self.assertLessEqual(bounds.get_y() + bounds.get_height(), app.window.get_height())
            self.assertEqual(app.connect_button.get_label(), "Отключить" if state else "Подключить")


if __name__ == "__main__":
    unittest.main(verbosity=2)
