#!/usr/bin/env python3
"""Small GTK smoke test; run in a graphical session or under Xvfb."""
from quantway import Quantway
from test_auth_helpers import block_auth_io
from unittest.mock import patch

def iter_children(widget):
    child = widget.get_first_child()
    while child:
        yield child
        child = child.get_next_sibling()


background_guard = patch("quantway.threading.Thread")
background_guard.start()
auth_guard = block_auth_io(Quantway)
stats_guard = patch.object(Quantway, "refresh_account_stats")
stats_guard.start()
app = Quantway()
assert app.register(None)
app.activate()
assert app.welcome_panel.get_visible()
assert not app.connection_panel.get_visible()
assert app.entry.get_parent() is None
assert not hasattr(app, "import_expander")
assert app.stack.get_child_by_name("external") is not None
assert app.stack.get_child_by_name("devices") is not None
assert app.stack.get_child_by_name("cabinet-section") is not None
app.entry.set_text("https://example.invalid/subscription")
profile = {"job_id": "one", "label": "Германия", "engine": "xray", "protocol": "VLESS"}
app.loaded_link = app.entry.get_text()
app.show_result([profile], "")
assert app.probe_button.get_sensitive()
app.show_probe([{"job_id": "one", "outcome": "ok", "elapsed_ms": 20, "job": {"engine": "xray"}, "control_url": "https://example.com/"}], "")
assert "1 из 1" in app.status.get_text()
assert app.connect_button.get_sensitive()
assert app.route_name.get_text() == "Германия"
assert "VLESS" in app.route_protocol.get_text()
assert app.connection_hint.get_text() == "Маршрут проверен"
assert app.route_choice.get_text() == "Выбран автоматически"
app.probe_running = True
app.show_progress(14, 56)
assert app.progress_percent.get_text() == "25 %"
app.probe_running = False
row = app.listbox.get_row_at_index(0)
name = row.get_child().get_first_child().get_first_child()
color = name.get_style_context().get_color()
assert min(color.red, color.green, color.blue) > 0.7, f"route label is too dark: {color}"
assert row.get_selectable()
assert app.routes_empty.get_visible() is False
profiles = [
    {"job_id": str(i), "label": f"Маршрут {i}", "protocol": "VLESS", "outcome": "ok", "elapsed_ms": 10 + i,
     "job": {"engine": "xray"}, "control_url": "https://example.com/"}
    for i in range(5)
]
app.show_result(profiles, "")
app.selected_profile = profiles[0]
app.update_route_card()
assert app.best_box.get_visible()
assert sum(1 for _ in iter_children(app.best_list)) == 3
app.choose_best(None, profiles[1])
assert app.route_name.get_text() == "Маршрут 1"
assert app.route_choice.get_text() == "Выбран вручную"
app.vpn_starting = True
app.update_best_routes()
assert not app.best_list.get_first_child().get_sensitive()
assert app.best_note.get_visible()
app.vpn_starting = False
app.nav_buttons[1].set_active(True)
assert app.stack.get_visible_child_name() == "routes"
app.nav_buttons[2].set_active(True)
assert app.stack.get_visible_child_name() == "account"
app.entry.set_text("https://different.invalid/")
assert not app.probe_button.get_sensitive()
assert not app.connect_button.get_sensitive()
app.show_devices({"cookie": "qw_session=test", "devices": [{"id": "x", "name": "Ноутбук"}]}, "")
assert not app.device_scroll.get_visible()
app.show_devices({"cookie": "qw_session=test", "needs_replacement": True, "devices": [{"id": "x", "name": "Ноутбук"}]}, "")
assert app.device_scroll.get_visible()
assert app.devices.get_first_child().get_next_sibling().get_label() == "Заменить: Ноутбук"
app.session = {"cookie": "qw_session=test", "identity": {"kind": "retail"}}
app.show_plans({"plans": [{"id": "p", "title": "Месяц", "price": "100 ₽", "totals": [{"devices": 1, "price": "150 ₽"}]}], "methods": [{"id": "m", "label": "Карта"}], "devices": {"current": 1, "max": 3}}, "")
assert app.plans_box.get_visible()
assert "150" in app.plan_price.get_text()
app.show_invoice({"invoice": {"id": "11111111-2222-4333-8444-555555555555", "title": "Счёт", "status": "Ожидает оплаты", "state": "awaiting", "pay_url": "https://pay.example/"}}, "")
assert "Ожидает оплаты" in app.status.get_text()
assert app.stack.get_visible_child_name() == "cabinet-section"
assert app.cabinet_title.get_text() == "Оплата счёта"
app.invoice_check_button.set_sensitive(False)
app.show_invoice({}, "Не удалось проверить оплату.")
assert app.invoice_check_button.get_sensitive()
app.nav_buttons[0].set_active(True)
app.entry.set_text(app.loaded_link)
app.show_probe([], "Не удалось проверить маршруты.")
assert app.results_stale
assert not app.connect_button.get_sensitive()
selected = app.selected_profile
app.select_route(app.listbox, app.listbox.get_row_at_index(0))
assert app.selected_profile is selected
app.show_login_form(None)
assert app.account_row.get_visible()
assert app.login_hint.get_visible()

# Account import must wait for the current route operation without changing its
# link. Completion (including failure/cancellation) then imports exactly once.
account_link = "https://example.invalid/account-subscription"
account = {"cookie": "qw_session=account-a", "identity": {"kind": "retail"},
           "account": {"subscription": {"days_left": 10}}, "current_device": "this-computer",
           "device_link": account_link}
for operation in ("probe", "fetch", "vpn"):
    app.entry.set_text("https://example.invalid/other-subscription")
    old_link = app.entry.get_text()
    app.loaded_link = old_link
    app.probe_running = operation == "probe"
    app.loading_profiles = operation == "fetch"
    app.vpn_process = object() if operation == "vpn" else None
    app.connected_since = 1 if operation == "vpn" else None
    app.show_devices(dict(account), "")
    assert app.entry.get_text() == old_link, operation
    assert app.pending_account_subscription, operation
    with patch.object(app, "load") as load:
        if operation == "probe":
            app.show_probe([], "Проверка остановлена.")
        elif operation == "fetch":
            app.show_result([], "Не удалось загрузить подписку.", old_link)
        else:
            app.vpn_process = None
            app.vpn_stopped("", True)
        load.assert_called_once_with(None)
    assert app.entry.get_text() == account_link, operation
    assert app.pending_account_subscription is None, operation

# Late callbacks from account A must not alter account B, navigate to its
# payment flow, or import a revoked device link after switching accounts.
old_context = app.account_context()
old_login_generation = app.account_generation
app.show_login_form(None)
app.show_devices(dict(account), "", old_login_generation)
assert app.session is None
assert app.account_row.get_visible()
new_account = {"cookie": "qw_session=account-b", "identity": {"kind": "retail"},
               "account": {"subscription": {"headline": "Аккаунт B", "days_left": 23}},
               "needs_replacement": True, "devices": [{"id": "old", "name": "Телефон B"}]}
app.show_devices(new_account, "")
app.nav_buttons[2].set_active(True)
before = app.status.get_text()
app.account_refreshed({"account": {"subscription": {"days_left": 99}}}, old_context)
app.account_refresh_failed(old_context)
app.show_plans({"plans": [], "methods": []}, "", old_context)
app.show_invoice({"invoice": {"state": "paid"}}, "", old_context)
app.replacement_done({"device_id": "wrong", "link": "https://wrong.invalid/"}, "", old_context)
assert app.session["cookie"] == "qw_session=account-b"
assert app.account_days.get_text() == "23 дня"
assert app.status.get_text() == before
assert app.stack.get_visible_child_name() == "account"
assert "device_link" not in app.session

# Worker requests use the cookie captured at dispatch, even if a new account
# becomes current before the worker starts running.
new_context = app.account_context()
with patch.object(app, "call_helper", return_value={"data": {}}) as helper, patch("quantway.GLib.idle_add") as idle:
    app.session = {"cookie": "qw_session=account-c"}
    app.refresh_account_worker(new_context, True)
    helper.assert_called_once_with("account-refresh", {"cookie": "qw_session=account-b", "bind_device": True})
    assert idle.call_args.args[-1] == new_context
app.session = new_account

# Refresh retries binding while all slots are occupied, then consumes the new
# binding and removes the replacement picker once a slot is available.
with patch("quantway.threading.Thread") as thread:
    app.refresh_cabinet(None)
    assert thread.call_args.kwargs["args"] == (new_context, True)
    assert not app.device_scroll.get_sensitive()
app.account_refreshed({"account": new_account["account"], "identity": new_account["identity"],
                       "binding": {"needs_replacement": True, "devices": [{"id": "new", "name": "Обновлённый телефон"}]}}, new_context)
assert app.device_scroll.get_visible()
assert app.devices.get_first_child().get_next_sibling().get_label() == "Заменить: Обновлённый телефон"
with patch.object(app, "load") as load:
    app.account_refreshed({"account": new_account["account"], "identity": new_account["identity"],
                           "binding": {"current_device": "bound", "device_link": account_link,
                                       "devices": [{"id": "bound", "name": "Этот компьютер"}]}}, new_context)
    load.assert_called_once_with(None)
assert not app.device_scroll.get_visible()
assert app.session["current_device"] == "bound"
assert not app.session["needs_replacement"]
assert app.session["current_device"] == "bound"
assert app.account_stats_panel.get_visible()
assert set(app.account_stat_values) == {"month", "self_month", "places", "online"}

# Rendering the cabinet while Quantway is active must retain a reusable action
# so switching to an external provider later still offers a way back.
previous_source = app.active_source
app.active_source = {"kind": "quantway", "id": app.session["current_device"], "name": "Quantway", "link": account_link}
app.render_account(app.session)
app.sync_controls()
assert not app.account_use_button.get_visible()
app.active_source = {"kind": "external", "id": "external-switch-fixture", "name": "Другой VPN", "link": "https://provider.invalid/sub"}
app.sync_controls()
assert app.account_use_button.get_visible()
app.active_source = previous_source
app.sync_controls()

# A third-party source has its own editor and identity. Account imports can
# change the active transport link without filling that editor with our token.
app.external_name.set_text("Рабочий VPN")
external_link = "https://provider.invalid/subscription?token=fixture"
app.external_url.set_text(external_link)
with patch.object(app, "load") as load:
    app.add_external_subscription(None)
    load.assert_called_once_with(None)
assert len(app.external_subscriptions) == 1
assert app.active_source["kind"] == "external"
assert app.active_source["name"] == "Рабочий VPN"
assert app.entry.get_text() == external_link
assert app.external_url.get_text() == ""
with patch.object(app, "load") as load:
    app.queue_account_subscription(account_link)
    load.assert_called_once_with(None)
assert app.active_source["kind"] == "quantway"
assert app.entry.get_text() == account_link
assert app.external_url.get_text() == ""
assert app.external_subscriptions[0]["link"] == external_link
app.nav_buttons[3].set_active(True)
assert app.stack.get_visible_child_name() == "external"
background_guard.stop()
stats_guard.stop()
auth_guard.stop()
app.window.destroy()
print("GTK smoke test passed")
