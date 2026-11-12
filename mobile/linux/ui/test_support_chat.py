#!/usr/bin/env python3
"""Offline GTK regressions for the native support chat and its request lifecycle."""
import base64
import unittest
from unittest.mock import Mock, patch

import gi
gi.require_version("Gtk", "4.0")
gi.require_version("Gdk", "4.0")
from gi.repository import Gdk, Gio, Gtk

from quantway import Quantway
from test_auth_helpers import block_auth_io
from qa_screens import settle


def messages(count=3):
    return [{"from_us": index % 2 == 0, "author": "Команда Quantway" if index % 2 == 0 else "Вы",
             "at": f"10:{index:02d}", "body": f"Сообщение {index}: " + "Проверяем подключение в вашей текущей сети. " * 3}
            for index in range(count)]


def fixture(items):
    return {"max_runes": 4000, "telegram": True,
            "current": {"id": 73, "state": "Переписка с поддержкой", "messages": items},
            "history": [{"id": 42, "subject": "Закрытое обращение", "closed": "24 октября"}]}


class NativeSupportChatTests(unittest.TestCase):
    def setUp(self):
        self.background = patch("quantway.threading.Thread")
        self.thread = self.background.start()
        self.app = app = Quantway()
        self.auth_guard = block_auth_io(app)
        self.stats_guard = patch.object(app, "refresh_account_stats")
        self.stats_guard.start()
        app.set_flags(Gio.ApplicationFlags.NON_UNIQUE)
        self.assertTrue(app.register(None))
        app.activate()
        self.helper = patch.object(app, "call_helper", side_effect=AssertionError("Real helper I/O is forbidden"))
        self.helper.start()
        app.session = {"cookie": "qw_session=support-offline-fixture", "identity": {"kind": "retail"},
                       "account": {"subscription": {"days_left": 20}, "features": {"support": True, "images": True}}}
        app.nav_buttons[2].set_active(True)
        app.render_cabinet_section("support", fixture(messages()))
        settle(100)
        self.thread.reset_mock()

    def tearDown(self):
        app = self.app
        app.closing = True
        app.stop_support_poll()
        app.window.destroy()
        app.quit()
        settle(20)
        self.stats_guard.stop()
        self.helper.stop()
        self.auth_guard.stop()
        self.background.stop()

    def test_staff_and_user_bubbles_have_distinct_alignment(self):
        app = self.app
        self.assertEqual(app.stack.get_visible_child_name(), "support-chat")
        staff = app.support_messages.get_first_child()
        customer = staff.get_next_sibling()
        self.assertTrue(staff.has_css_class("from-us"))
        self.assertEqual(staff.get_halign(), Gtk.Align.START)
        self.assertTrue(customer.has_css_class("from-user"))
        self.assertEqual(customer.get_halign(), Gtk.Align.END)
        self.assertFalse(app.support_text.get_accepts_tab())

    def test_quiet_poll_does_not_block_composer_and_send_preempts_it(self):
        app = self.app
        app.support_buffer.set_text("Новый вопрос")
        old_done = Mock()
        self.assertTrue(app.support_request("GET", "support", None, old_done, quiet=True))
        old_serial = app._support_serial
        self.assertTrue(app.support_send_button.get_sensitive())
        self.assertTrue(app.support_attach_button.get_sensitive())
        self.assertFalse(app.support_chat_spinner.get_property("spinning"))
        new_done = Mock()
        self.assertTrue(app.support_request("POST", "support/messages", {"body": "Новый вопрос"}, new_done))
        new_serial = app._support_serial
        self.assertGreater(new_serial, old_serial)
        self.assertFalse(app.support_send_button.get_sensitive())
        self.assertTrue(app.support_chat_spinner.get_property("spinning"))
        app.support_response(app.account_context(), old_serial, fixture([]), "", old_done, True)
        old_done.assert_not_called()
        self.assertTrue(app._support_busy)
        self.assertEqual(app.support_draft(), "Новый вопрос")
        app.support_response(app.account_context(), new_serial, {}, "", new_done)
        new_done.assert_called_once_with({})
        self.assertFalse(app._support_busy)
        self.assertFalse(app.support_chat_spinner.get_property("spinning"))

    def test_send_preserves_draft_until_ack_and_does_not_erase_new_typing(self):
        app = self.app
        raw = "  Не подключается VPN\n"
        app.support_buffer.set_text(raw)
        with patch.object(app, "support_request", return_value=True) as request:
            self.assertTrue(app.send_support_text(None))
            self.assertEqual(request.call_args.args[:3], ("POST", "support/messages", {"body": "Не подключается VPN"}))
            acknowledge = request.call_args.args[3]
        self.assertEqual(app.support_draft(), raw)
        app.support_buffer.set_text("Следующий вопрос")
        with patch.object(app, "refresh_support_chat") as refresh:
            acknowledge({})
            refresh.assert_called_once_with()
        self.assertEqual(app.support_draft(), "Следующий вопрос")
        app.support_buffer.set_text(raw)
        with patch.object(app, "refresh_support_chat"):
            acknowledge({})
        self.assertEqual(app.support_draft(), "")
        with patch.object(app, "support_request", return_value=True) as request:
            app.send_support_image(b"offline-image", "Подпись", "Подпись")
            self.assertEqual(request.call_args.args[:3], ("POST", "support/images", {
                "image_base64": base64.b64encode(b"offline-image").decode(), "caption": "Подпись"}))

    def test_hidden_get_cannot_navigate_but_hidden_send_ack_can_clear_sent_draft(self):
        app = self.app
        app.support_buffer.set_text("Отправленный вопрос")
        done = Mock()
        self.assertTrue(app.support_request("GET", "support", None, done))
        context, serial = app.account_context(), app._support_serial
        app.stack.set_visible_child_name("home")
        app.support_response(context, serial, fixture([]), "", done)
        done.assert_not_called()
        self.assertEqual(app.stack.get_visible_child_name(), "home")
        self.assertEqual(app.support_draft(), "Отправленный вопрос")
        app.stack.set_visible_child_name("support-chat")
        acknowledge = lambda data: app.support_message_sent(data, "Отправленный вопрос")
        self.assertTrue(app.support_request("POST", "support/messages", {"body": "Отправленный вопрос"}, acknowledge))
        worker = self.thread.call_args.kwargs["target"]
        app.stack.set_visible_child_name("home")
        with patch.object(app, "call_helper", return_value={"data": {}}), patch("support_ui.GLib.idle_add") as idle:
            worker()
        callback, *arguments = idle.call_args.args
        self.assertTrue(arguments[-1])  # The POST worker enables hidden acknowledgements.
        with patch.object(app, "refresh_support_chat") as refresh:
            callback(*arguments)
            refresh.assert_not_called()
        self.assertEqual(app.stack.get_visible_child_name(), "home")
        self.assertEqual(app.support_draft(), "")

    def test_new_messages_preserve_reader_scroll_and_offer_jump_to_latest(self):
        app = self.app
        app.render_support_chat(fixture(messages(30)))
        settle(150)
        adjustment = app.support_scroll.get_vadjustment()
        self.assertGreater(adjustment.get_upper(), adjustment.get_page_size() + 150)
        adjustment.set_value(100)
        app.support_buffer.set_text("Черновик без отправки")
        app.render_support_chat(fixture(messages(31)))
        settle(150)
        self.assertAlmostEqual(adjustment.get_value(), 100, delta=1)
        self.assertEqual(app.support_draft(), "Черновик без отправки")
        self.assertTrue(app.support_unread_button.get_visible())
        app.support_jump_to_latest()
        self.assertAlmostEqual(adjustment.get_value(), adjustment.get_upper() - adjustment.get_page_size(), delta=1)
        self.assertFalse(app.support_unread_button.get_visible())

    def test_enter_send_shift_newline_and_local_character_limit(self):
        app = self.app
        self.assertFalse(app.support_text.get_accepts_tab())
        before = app.status.get_text()
        self.assertTrue(app.support_key_pressed(None, Gdk.KEY_Return, 0, Gdk.ModifierType(0)))
        self.assertEqual(app.status.get_text(), before)
        app.support_buffer.set_text("Вопрос")
        with patch.object(app, "send_support_text") as send:
            self.assertFalse(app.support_key_pressed(None, Gdk.KEY_Return, 0, Gdk.ModifierType.SHIFT_MASK))
            send.assert_not_called()
            self.assertTrue(app.support_key_pressed(None, Gdk.KEY_Return, 0, Gdk.ModifierType(0)))
            send.assert_called_once_with(None)
        app.support_buffer.set_text("я" * 4001)
        self.assertFalse(app.support_send_button.get_sensitive())
        with patch.object(app, "support_request") as request:
            self.assertFalse(app.send_support_text(None))
            request.assert_not_called()
        self.assertEqual(len(app.support_draft()), 4001)

    def test_closed_ticket_is_read_only_and_timer_stops_on_hide_or_close(self):
        app = self.app
        self.assertIsNotNone(app._support_timer)
        app.show_support_ticket({"ticket": {"id": 42, "closed": True, "state": "Обращение закрыто."}, "messages": messages()})
        self.assertFalse(app.support_editor.get_visible())
        self.assertFalse(app.support_composer_actions.get_visible())
        self.assertTrue(app.support_return_current.get_visible())
        app.support_buffer.set_text("Не должно отправиться в закрытое обращение")
        with patch.object(app, "support_request") as request:
            self.assertFalse(app.send_support_text(None))
            request.assert_not_called()
        app.stack.set_visible_child_name("home")
        self.assertIsNone(app._support_timer)
        app.stack.set_visible_child_name("support-chat")
        self.assertIsNotNone(app._support_timer)
        app.close_window(app.window)
        self.assertIsNone(app._support_timer)

    def test_explicit_open_focuses_once_while_quiet_updates_do_not_steal_focus(self):
        app = self.app
        app.stack.set_visible_child_name("home")
        app.status.set_text("Предыдущее сообщение")
        with patch.object(app.support_text, "grab_focus", return_value=True) as focus:
            self.assertTrue(app.open_support_chat())
            self.assertEqual(app.status.get_text(), "")
            settle(80)
            focus.assert_called_once_with()
            app.support_response(app.account_context(), app._support_serial, fixture(messages()), "", app.render_support_chat)
            settle(80)
            focus.assert_called_once_with()
            app.status.set_text("Черновик сохранён в приложении")
            self.assertTrue(app.support_request("GET", "support", None, app.render_support_chat, quiet=True))
            app.support_response(app.account_context(), app._support_serial, fixture(messages()), "", app.render_support_chat, True)
            settle(80)
            focus.assert_called_once_with()
            self.assertEqual(app.status.get_text(), "Черновик сохранён в приложении")
            app.stack.set_visible_child_name("home")
            self.assertTrue(app.open_support_chat())
            app.stack.set_visible_child_name("home")
            settle(80)
            focus.assert_called_once_with()

    def test_worker_keeps_dispatch_cookie_and_old_account_response_is_ignored(self):
        app = self.app
        done = Mock()
        self.assertTrue(app.support_request("GET", "support", None, done))
        worker = self.thread.call_args.kwargs["target"]
        old_context, serial = app.account_context(), app._support_serial
        app.session = dict(app.session, cookie="qw_session=another-offline-account")
        app.account_generation += 1
        before = app.support_draft()
        with patch.object(app, "call_helper", return_value={"data": fixture([])}) as helper, patch("support_ui.GLib.idle_add"):
            worker()
            self.assertEqual(helper.call_args.args[1]["cookie"], "qw_session=support-offline-fixture")
        app.support_response(old_context, serial, fixture([]), "", done)
        done.assert_not_called()
        self.assertEqual(app.support_draft(), before)


if __name__ == "__main__":
    unittest.main(verbosity=2)
