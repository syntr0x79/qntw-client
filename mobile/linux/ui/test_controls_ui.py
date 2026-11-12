#!/usr/bin/env python3
"""Native control focus/semantics and wrapping; no application/network workers."""
import json
import os
from pathlib import Path
import time
import unittest

import gi
gi.require_version("Gtk", "4.0")
gi.require_version("Gdk", "4.0")
from gi.repository import Gdk, GLib, Gtk

from desktop_ui import action_row, box, button, field, heading, icon_button, label, status_label


def settle(milliseconds=60):
    end = time.monotonic() + milliseconds / 1000
    context = GLib.MainContext.default()
    while time.monotonic() < end:
        while context.pending():
            context.iteration(False)
        time.sleep(0.005)


class ControlTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        Gtk.init()
        cls.css = Gtk.CssProvider()
        cls.css.load_from_path(str(Path(__file__).with_name("desktop.css")))
        Gtk.StyleContext.add_provider_for_display(Gdk.Display.get_default(), cls.css,
                                                  Gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)

    def setUp(self):
        self.window = Gtk.Window(title="Offline controls")
        self.window.add_css_class("quantway")
        self.window.set_default_size(740, 620)
        self.page = box(12)
        self.page.add_css_class("workspace")
        for side in ("start", "end", "top", "bottom"):
            getattr(self.page, "set_margin_" + side)(24)
        self.window.set_child(self.page)

    def tearDown(self):
        self.window.destroy()
        settle(20)

    def show(self):
        self.window.present()
        settle()

    def tab(self, backwards=False):
        # This is GTK's native Tab/Shift+Tab action, rather than grabbing the
        # expected target (which would hide a container's broken traversal).
        direction = Gtk.DirectionType.TAB_BACKWARD if backwards else Gtk.DirectionType.TAB_FORWARD
        self.window.emit("move-focus", direction)
        settle(5)
        return self.window.get_focus()

    def row(self):
        before = button("До строки", lambda _b: None)
        actions = [button(caption, lambda _b: None) for caption in ("Первое", "Второе", "Третье")]
        row = action_row(*actions)
        after = button("После строки", lambda _b: None)
        for item in (before, row, after):
            self.page.append(item)
        self.show()
        return before, actions, row, after

    def test_native_tab_skips_disabled_first_middle_last_and_leaves_row(self):
        before, actions, _row, after = self.row()
        for disabled in ((), (0,), (1,), (2,), (0, 1), (1, 2), (0, 2), (0, 1, 2)):
            with self.subTest(disabled=disabled):
                for i, action in enumerate(actions):
                    action.set_sensitive(i not in disabled)
                expected = [action for action in actions if action.is_sensitive()]
                before.grab_focus()
                for target in expected + [after, before] + expected + [after]:
                    self.assertIs(self.tab(), target)
                after.grab_focus()
                for target in list(reversed(expected)) + [before, after] + list(reversed(expected)) + [before]:
                    self.assertIs(self.tab(backwards=True), target)

    def test_visibility_wrappers_leave_no_focus_stop_or_blank_action(self):
        before, actions, row, after = self.row()
        for hidden in ((0,), (1,), (2,), (0, 1, 2), ()):
            with self.subTest(hidden=hidden):
                for i, action in enumerate(actions):
                    action.set_visible(i not in hidden)
                    self.assertEqual(row.get_child_at_index(i).get_visible(), i not in hidden)
                    self.assertFalse(row.get_child_at_index(i).get_focusable())
                settle(10)
                before.grab_focus()
                for target in [a for a in actions if a.get_visible()] + [after]:
                    self.assertIs(self.tab(), target)

    def test_menu_button_remains_a_single_native_focus_stop(self):
        before = button("До", lambda _b: None)
        menu = Gtk.MenuButton(icon_name="view-more-symbolic")
        popover = Gtk.Popover()
        popover.set_child(button("Внутри меню", lambda _b: None))
        menu.set_popover(popover)
        after = button("После", lambda _b: None)
        for item in (before, action_row(menu), after):
            self.page.append(item)
        self.show()
        before.grab_focus()
        focused = self.tab()
        self.assertTrue(focused is menu or focused.is_ancestor(menu))
        self.assertIs(self.tab(), after)
        focused = self.tab(backwards=True)
        self.assertTrue(focused is menu or focused.is_ancestor(menu))
        self.assertIs(self.tab(backwards=True), before)

    def test_buttons_keep_native_activation_and_callback_without_wrapper_activation(self):
        calls = []
        action = button("Выполнить", lambda widget: calls.append(widget))
        self.page.append(action_row(action))
        self.show()
        action.grab_focus()
        self.assertTrue(action.activate())
        settle(300)
        self.assertEqual(calls, [action])
        self.assertIs(self.window.get_focus(), action)

    def test_semantics_are_explicit_and_passive_peer_labels_stay_passive(self):
        page = heading("Подключение")
        section = heading("Страна", level=2)
        ordinary = label("Подключён")
        status = status_label("Подключаем…")
        alert = status_label("Доступ истёк", urgent=True)
        self.assertEqual(page.get_accessible_role(), Gtk.AccessibleRole.HEADING)
        self.assertEqual(section.get_accessible_role(), Gtk.AccessibleRole.HEADING)
        self.assertEqual(ordinary.get_accessible_role(), Gtk.AccessibleRole.LABEL)
        self.assertEqual(status.get_accessible_role(), Gtk.AccessibleRole.STATUS)
        self.assertEqual(alert.get_accessible_role(), Gtk.AccessibleRole.ALERT)
        self.assertTrue(page.has_css_class("page-title"))
        self.assertTrue(section.has_css_class("section-title"))
        if os.environ.get("GTK_A11Y") == "test":
            self.assertTrue(Gtk.test_accessible_has_property(page, Gtk.AccessibleProperty.LEVEL))
            self.assertTrue(Gtk.test_accessible_has_property(section, Gtk.AccessibleProperty.LEVEL))
        for invalid in (0, 7, True, "2"):
            with self.subTest(level=invalid), self.assertRaises(ValueError):
                heading("Неверный уровень", level=invalid)

    def test_field_retains_mnemonic_and_accessible_label(self):
        entry = Gtk.Entry()
        container = field("_Название", entry)
        caption = container.get_first_child()
        self.assertIs(caption.get_mnemonic_widget(), entry)
        self.assertEqual(caption.get_text(), "Название")
        self.assertIn(caption, entry.list_mnemonic_labels())
        self.assertIs(container.get_last_child(), entry)
        if os.environ.get("GTK_A11Y") == "test":
            self.assertTrue(Gtk.test_accessible_has_property(entry, Gtk.AccessibleProperty.LABEL))
        self.page.append(container)
        self.show()
        self.assertTrue(entry.get_sensitive())

    def test_unchanged_lifecycle_poll_does_not_notify_status_or_alert_again(self):
        for urgent in (False, True):
            with self.subTest(urgent=urgent):
                status = status_label("Подключён", urgent=urgent)
                notifications = []
                status.connect("notify::label", lambda widget, _p: notifications.append(widget.get_text()))
                for _ in range(10):
                    status.set_text("Подключён")
                self.assertEqual(notifications, [])
                status.set_text("Доступ истёк")
                self.assertEqual(notifications, ["Доступ истёк"])
                for _ in range(10):
                    status.set_text("Доступ истёк")
                self.assertEqual(notifications, ["Доступ истёк"])
                status.set_text("")
                self.assertEqual(notifications, ["Доступ истёк", ""])

    def test_icon_action_has_accessible_name_and_minimum_target(self):
        action = icon_button("view-refresh-symbolic", "Обновить данные", lambda _b: None)
        self.page.append(action)
        self.show()
        self.assertEqual(action.get_tooltip_text(), "Обновить данные")
        self.assertEqual(action.get_size_request(), (36, 36))
        if os.environ.get("GTK_A11Y") == "test":
            self.assertTrue(Gtk.test_accessible_has_property(action, Gtk.AccessibleProperty.LABEL))
        ok, bounds = action.compute_bounds(self.window)
        self.assertTrue(ok)
        self.assertGreaterEqual(bounds.get_width(), 36)
        self.assertGreaterEqual(bounds.get_height(), 36)

    def test_action_wrapping_geometry_at_740_with_200_percent_text(self):
        settings = Gtk.Settings.get_default()
        original = settings.get_property("gtk-xft-dpi")
        records = []
        try:
            for scale in (1, 2):
                settings.set_property("gtk-xft-dpi", 96 * 1024 * scale)
                panel = box(12, style="panel")
                controls = [button(caption, lambda _b: None) for caption in
                            ("Подключить", "Проверить маршруты", "Состояние меша")]
                panel.append(action_row(*controls))
                self.page.append(panel)
                self.show()
                self.assertLessEqual(self.window.get_width(), 740)
                geometry = []
                for action in controls:
                    ok, rect = action.compute_bounds(self.window)
                    self.assertTrue(ok)
                    self.assertGreaterEqual(rect.get_x(), 0)
                    self.assertLessEqual(rect.get_x() + rect.get_width(), self.window.get_width())
                    self.assertGreaterEqual(rect.get_height(), 34)
                    geometry.append([rect.get_x(), rect.get_y(), rect.get_width(), rect.get_height()])
                # No two hit targets overlap even after a native row wraps.
                for a, b in zip(geometry, geometry[1:]):
                    self.assertTrue(a[0] + a[2] <= b[0] or a[1] + a[3] <= b[1])
                record = {"scale": scale, "window": [self.window.get_width(), self.window.get_height()],
                          "buttons": geometry}
                output = os.environ.get("QUANTWAY_CONTROL_QA_OUTPUT")
                if output:
                    from qa_screens import capture
                    folder = Path(output)
                    folder.mkdir(parents=True, exist_ok=True)
                    capture(self.window, folder / f"controls-{scale}x.png", "auto")
                records.append(record)
                self.page.remove(panel)
        finally:
            settings.set_property("gtk-xft-dpi", original)
        output = os.environ.get("QUANTWAY_CONTROL_QA_OUTPUT")
        if output:
            (Path(output) / "controls-geometry.json").write_text(json.dumps(records, indent=2))


if __name__ == "__main__":
    unittest.main()
