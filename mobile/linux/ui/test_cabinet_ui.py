#!/usr/bin/env python3
"""Exercise native cabinet click paths with isolated, mocked account I/O."""
from unittest.mock import Mock, patch

from quantway import Quantway
from test_auth_helpers import block_auth_io
from gi.repository import Gtk


def descendants(widget):
    yield widget
    child = widget.get_first_child()
    while child:
        yield from descendants(child)
        child = child.get_next_sibling()


def captioned(widget, text):
    return next(item for item in descendants(widget) if isinstance(item, Gtk.Button) and
                (button_caption(item) == text or item.get_tooltip_text() == text))


def button_caption(widget):
    if widget.get_label():
        return widget.get_label()
    return next((item.get_text() for item in descendants(widget)
                 if isinstance(item, Gtk.Label) and item.has_css_class("row-title")), None)


def expand_actions():
    for item in descendants(app.account_links):
        if isinstance(item, Gtk.Expander):
            item.set_expanded(True)


def dialog():
    windows = Gtk.Window.get_toplevels()
    return next(windows.get_item(index) for index in range(windows.get_n_items())
                if isinstance(windows.get_item(index), Gtk.MessageDialog))


auth_guard = block_auth_io(Quantway)
stats_guard = patch.object(Quantway, "refresh_account_stats")
stats_guard.start()
app = Quantway()
assert app.register(None)
with patch("quantway.threading.Thread"):
    app.activate()
account = {"cookie": "qw_session=cabinet-fixture", "identity": {"kind": "retail"}, "account": {
    "subscription": {"days_left": 20, "devices": 3, "status": "active"},
    "features": {"promo": True, "support": True, "images": True, "referral": True}, "telegram": True}}
app.session = account
app.render_account(account)
assert captioned(app.account_links, "Мои устройства")
assert captioned(app.account_links, "Поддержка")

# Existing invoice rows and pagination dispatch exact routes, including an
# escaped opaque cursor, without contacting either payment service or helper.
invoice = {"id": "11111111-1111-4111-8111-111111111111", "title": "На месяц", "amount_rub": "299 ₽", "date": "31 октября", "status": "Ожидает оплаты", "state": "awaiting", "is_fiat": True}
app.render_cabinet_section("invoices", {"invoices": [invoice], "more": True, "next_before": "2026-10-31T12:30:00+03:00"})
with patch.object(app, "cabinet_request") as request:
    captioned(app.cabinet_content, invoice["title"]).emit("clicked")
    assert request.call_args.args[:2] == ("GET", "invoices/" + invoice["id"])
    captioned(app.cabinet_content, "Следующая страница").emit("clicked")
    assert "%2B03%3A00" in request.call_args.args[1]
app.show_account_invoice({"invoice": dict(invoice, pay_url="https://pay.example.invalid/fixture")})
assert any(isinstance(item, Gtk.Label) and item.has_css_class("invoice-amount") and item.get_text() == "299 ₽" for item in descendants(app.cabinet_content))
with patch.object(app, "open_safe_url") as external:
    captioned(app.cabinet_content, "Перейти к оплате").emit("clicked")
    external.assert_called_once_with("https://pay.example.invalid/fixture")
with patch("cabinet_ui.threading.Thread") as thread:
    app.show_account_invoice({"invoice": dict(invoice, state="paid")})
    assert thread.call_args.kwargs["target"] == app.refresh_account_worker
    primary = [item for item in descendants(app.cabinet_content)
               if isinstance(item, Gtk.Button) and item.has_css_class("primary")]
    assert [button_caption(item) for item in primary] == ["Вернуться в кабинет"]
    assert not any(isinstance(item, Gtk.Button) and button_caption(item) == "Перейти к оплате"
                   for item in descendants(app.cabinet_content))

# A payment under review offers a status check, never a second payment action,
# even if a response still contains the old payment URL.
app.show_account_invoice({"invoice": dict(invoice, state="review", pay_url="https://pay.example.invalid/old")})
primary = [item for item in descendants(app.cabinet_content)
           if isinstance(item, Gtk.Button) and item.has_css_class("primary")]
assert [button_caption(item) for item in primary] == ["Проверить оплату"]
with patch.object(app, "cabinet_request") as request:
    primary[0].emit("clicked")
    assert request.call_args.args[:2] == ("GET", "invoices/" + invoice["id"])

# Moving account links into clearer groups cannot expose retail-only actions
# to the owner identity.
app.session = dict(account, identity={"kind": "owner"})
app.build_account_links()
expand_actions()
captions = {button_caption(item) for item in descendants(app.account_links) if isinstance(item, Gtk.Button)}
assert "Счета и оплаты" in captions
assert not {"Мои устройства", "История подписки", "Пригласить друга", "Прокси для Telegram",
            "Приостановить подписку", "Возобновить подписку"}.intersection(captions)
app.session = account
app.build_account_links()

app.render_cabinet_section("history", {"moves": [{"delta": -2, "what": "Пауза", "when": "30 октября"}]})
assert any(isinstance(item, Gtk.Label) and item.get_text() == "−2 дня" for item in descendants(app.cabinet_content))
assert any(isinstance(item, Gtk.Label) and item.get_text() == "Пауза" for item in descendants(app.cabinet_content))
assert any(isinstance(item, Gtk.Label) and item.get_text() == "30 октября" for item in descendants(app.cabinet_content))
app.render_cabinet_section("referral", {"invited": 2, "days_earned": 10, "share_text": "Приглашение", "link": "https://qntw.link/friend/fixture"})
with patch.object(app, "copy_cabinet_text") as copy:
    captioned(app.cabinet_content, "Скопировать приглашение").emit("clicked")
    assert copy.call_args.args[0] == "Приглашение\nhttps://qntw.link/friend/fixture"

# Blank/oversized support text is rejected locally; valid input is trimmed.
support = {"max_runes": 4000, "current": {"messages": [{"author": "Поддержка", "from_us": True, "at": "Сегодня", "body": "Ответ", "images": [77]}]},
           "history": [{"id": 42, "subject": "Старое обращение", "closed": "24 октября"}]}
app.render_cabinet_section("support", support)
chat = app.stack.get_child_by_name("support-chat")
assert app.stack.get_visible_child_name() == "support-chat"
text = app.support_text
assert not text.get_accepts_tab()
assert app.support_messages.get_first_child().get_halign() == Gtk.Align.START
with patch.object(app, "support_request") as request:
    captioned(chat, "Отправить сообщение").emit("clicked")
    request.assert_not_called()
    text.get_buffer().set_text("я" * 4001)
    captioned(chat, "Отправить сообщение").emit("clicked")
    request.assert_not_called()
    text.get_buffer().set_text("  Не проходит проверка маршрутов  ")
    captioned(chat, "Отправить сообщение").emit("clicked")
    assert request.call_args.args[:3] == ("POST", "support/messages", {"body": "Не проходит проверка маршрутов"})
    captioned(chat, "Открыть изображение").emit("clicked")
    assert request.call_args.args[:2] == ("GET", "support/attachments/77")
    captioned(app.support_history_list, "Старое обращение · 24 октября").emit("clicked")
    assert request.call_args.args[:2] == ("GET", "support/tickets/42")
app.show_support_ticket({"ticket": {"id": 42, "subject": "Старое обращение", "closed": True}, "messages": support["current"]["messages"]})
assert captioned(chat, "Перейти к текущему обращению")
assert not app._support_can_write
assert not app.support_editor.get_visible()
app.show_support_image({"image_base64": "invalid-image"})
assert "Не удалось открыть" in app.status.get_text()

# A tariff refresh must immediately retire the visible old price and invoice
# action. Failed requests expose retry, never the stale purchasable form.
plan_fixture = {"plans": [{"id": "month", "title": "Месяц", "price": "299 ₽"}],
                "methods": [{"id": "card", "label": "Карта"}]}
app.show_plans(plan_fixture, "")
old_invoice_button = app.invoice_button
with patch("quantway.threading.Thread"):
    app.load_plans(None)
    refresh_context = app.account_context(renewal=True)
    assert not app.plans_box.get_visible()
    assert not app.plans_box.get_sensitive()
    assert not old_invoice_button.is_sensitive()
    app.show_plans({}, "Не удалось загрузить тарифы.", refresh_context)
    assert app.plans_retry.get_visible()
    assert not app.plans_box.get_visible()
    assert not old_invoice_button.is_sensitive()

# A late tariff response cannot navigate back after the user leaves, nor can
# an obsolete renewal context overwrite the newer form or its retry state.
with patch("quantway.threading.Thread"):
    app.load_plans(None)
    current_context = app.account_context(renewal=True)
app.stack.set_visible_child_name("home")
app.show_plans(plan_fixture, "", current_context)
assert app.stack.get_visible_child_name() == "home"
assert app.plans_box.get_sensitive()
assert not app.plans_retry.get_visible()
app.show_plans({}, "Запоздалая ошибка", refresh_context)
assert app.stack.get_visible_child_name() == "home"
assert app.plans_box.get_sensitive()
assert not app.plans_retry.get_visible()

# Completing an invoice request must not replace a conversation the user
# opened while the payment helper was working in the background.
app.stack.set_visible_child_name("support-chat")
app.invoice_button.set_sensitive(False)
with patch.object(app, "call_helper", side_effect=AssertionError("No helper may run")) as helper, patch("quantway.threading.Thread") as thread:
    app.show_invoice({"invoice": invoice}, "", current_context, "renew")
    assert app.stack.get_visible_child_name() == "support-chat"
    assert app.invoice_button.get_sensitive()
    helper.assert_not_called()
    thread.assert_not_called()
app.stack.set_visible_child_name("cabinet-section")

# Promo edits are only submitted on an explicit action and honour feature flags.
while child := app.plans_box.get_first_child():
    app.plans_box.remove(child)
app.append_promo_controls({"promo": {"code": "OLD"}})
entry = next(item for item in descendants(app.plans_box) if isinstance(item, Gtk.Entry))
with patch.object(app, "cabinet_request") as request:
    captioned(app.plans_box, "Применить промокод").emit("clicked")
    request.assert_not_called()
    entry.set_text("  SAVE  ")
    captioned(app.plans_box, "Применить промокод").emit("clicked")
    assert request.call_args.args[:3] == ("POST", "promo", {"code": "SAVE"})
    captioned(app.plans_box, "Убрать промокод").emit("clicked")
    assert request.call_args.args[:2] == ("DELETE", "promo")

with patch.object(app, "cabinet_request") as request:
    app.confirm_subscription_pause(False)
    request.assert_not_called()
    assert dialog().get_widget_for_response(Gtk.ResponseType.ACCEPT).get_label() == "Приостановить"
    assert dialog().get_widget_for_response(Gtk.ResponseType.ACCEPT).has_css_class("danger-action")
    dialog().response(Gtk.ResponseType.CANCEL)
    request.assert_not_called()
    app.confirm_subscription_pause(True)
    assert dialog().get_widget_for_response(Gtk.ResponseType.ACCEPT).get_label() == "Возобновить"
    assert dialog().get_widget_for_response(Gtk.ResponseType.ACCEPT).has_css_class("primary")
    dialog().response(Gtk.ResponseType.ACCEPT)
    assert request.call_args.args[:2] == ("POST", "subscription/resume")

app.render_cabinet_section("tg-proxy", {"state": "ready", "links": [{"host": "Узел", "url": "https://t.me/proxy?fixture=1", "tg": "tg://proxy?fixture=1"}]})
with patch.object(app, "open_safe_url") as external:
    captioned(app.cabinet_content, "Открыть в Telegram").emit("clicked")
    external.assert_called_once_with("https://t.me/proxy?fixture=1", telegram=True)

# A helper worker captures its dispatch cookie; late callbacks cannot mutate a
# replacement account or revive a disabled stale page.
with patch("cabinet_ui.threading.Thread") as thread:
    assert app.cabinet_request("GET", "support", done=Mock())
    worker = thread.call_args.kwargs["target"]
    old_context, old_serial = app.account_context(), app.cabinet_serial
    app.session = dict(account, cookie="qw_session=another-account")
    app.account_generation += 1
    with patch.object(app, "call_helper", return_value={"data": {}}) as helper, patch("cabinet_ui.GLib.idle_add"):
        worker()
        assert helper.call_args.args[1]["cookie"] == "qw_session=cabinet-fixture"
    done = Mock()
    app.cabinet_response(old_context, old_serial, {"message": "old account"}, "", done)
    done.assert_not_called()
app.cabinet_busy = False
app.cabinet_content.set_sensitive(True)
app.account_links.set_sensitive(True)

# Role and feature gates match the web cabinet: member/owner accounts do not
# receive retail-only history/referral/pause links even if a flag is enabled.
for role in ("member", "owner"):
    app.session = dict(account, identity={"kind": role})
    app.build_account_links()
    expand_actions()
    captions = {button_caption(item) for item in descendants(app.account_links) if isinstance(item, Gtk.Button)}
    assert "История подписки" not in captions
    assert "Пригласить друга" not in captions
    assert "Приостановить подписку" not in captions
app.session = dict(account, account=dict(account["account"], features={}))
app.build_account_links()
expand_actions()
captions = {button_caption(item) for item in descendants(app.account_links) if isinstance(item, Gtk.Button)}
assert "Поддержка" not in captions
assert "Пригласить друга" not in captions
app.session = dict(account, account=dict(account["account"], subscription={"status": "paused"}))
app.build_account_links()
expand_actions()
assert captioned(app.account_links, "Возобновить подписку")
app.session = account

# Broken/unsafe server links cannot raise an exception or launch an app.
with patch("cabinet_ui.Gio.AppInfo.launch_default_for_uri") as external:
    for value in ("https://[invalid", "file:///etc/passwd", "https://name:secret@example.invalid/"):
        app.open_safe_url(value)
    external.assert_not_called()
app.render_cabinet_section("tg-proxy", {"state": "unavailable", "links": []})
captions = {item.get_label() for item in descendants(app.cabinet_content) if isinstance(item, Gtk.Button)}
assert "Обновить ключи прокси" not in captions

# A promo or other cabinet request locks the renewal form too. Navigating away
# while loading does not pull the user back to the old section on completion.
app.stack.set_visible_child_name("cabinet-section")
with patch("cabinet_ui.threading.Thread"):
    done = Mock()
    assert app.cabinet_request("GET", "support", done=done)
    assert not app.plans_box.get_sensitive()
    context, serial = app.account_context(), app.cabinet_serial
    app.stack.set_visible_child_name("home")
    app.cabinet_response(context, serial, {}, "", done, "cabinet-section")
    done.assert_not_called()
    assert app.stack.get_visible_child_name() == "home"
    assert app.plans_box.get_sensitive()
    assert not app.cabinet_busy

# The API refreshes this session when issuing a new login code. Preserve the
# account with that fresh cookie, while invalidating outstanding old callbacks.
app.session = dict(account, cookie="qw_session=revoked-fixture")
app.build_account_links()
expand_actions()
with patch.object(app, "call_helper", side_effect=ValueError("Войдите заново")) as helper:
    captioned(app.account_links, "Войти заново").emit("clicked")
    helper.assert_not_called()
assert app.session is None
assert app.account_row.get_visible()
app.session = account
app.render_account(account)
old_context = app.account_context()
app.show_new_token({"code": "OFFLINE-NEW-CODE", "session_cookie": "qw_session=fresh-cookie"})
assert app.session["cookie"] == "qw_session=fresh-cookie"
assert app.session["identity"]["kind"] == "retail"
assert not app.account_callback_current(old_context)
assert captioned(app.cabinet_content, "Вернуться в кабинет")

app.render_cabinet_section("support", dict(support, reopen={"id": 42, "deadline": "2026-11-01T12:00:00Z"}))
assert "недавно закрытое" in app.support_reply_context.get_text()

# Logout removes the Quantway session while preserving third-party providers.
app.external_subscriptions = [{"id": "external-test", "name": "Другой провайдер", "link": "https://example.invalid/sub/fixture"}]
app.finish_account_logout()
assert app.session is None
assert len(app.external_subscriptions) == 1
assert app.account_row.get_visible()
app.build_account_links()
assert not app.account_links.get_visible()
app.stop_support_poll()
app.window.destroy()
stats_guard.stop()
auth_guard.stop()
print("Cabinet UI regression passed")
