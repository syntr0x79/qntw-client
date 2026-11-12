#!/usr/bin/env python3
"""Offline regression checks for account device operations; run under Xvfb."""
import base64
from unittest.mock import Mock, patch

from device_ui import DeviceMixin, device_presence, format_usage
from quantway import Quantway
from test_auth_helpers import block_auth_io
from gi.repository import Gtk


def descendants(widget):
    yield widget
    child = widget.get_first_child()
    while child:
        yield from descendants(child)
        child = child.get_next_sibling()


if not issubclass(Quantway, DeviceMixin):
    class TestQuantway(DeviceMixin, Quantway):
        pass
else:
    TestQuantway = Quantway

auth_guard = block_auth_io(TestQuantway)
stats_guard = patch.object(TestQuantway, "refresh_account_stats")
stats_guard.start()
app = TestQuantway()
assert app.register(None)
with patch("quantway.threading.Thread"):
    app.activate()
if not hasattr(app, "device_list"):
    app.stack.add_named(app.build_devices_page(), "devices")
current = {"id": "11111111-1111-4111-8111-111111111111", "name": "Рабочий компьютер", "platform": "linux", "online": True, "month_bytes": 2048}
blocked = {"id": "22222222-2222-4222-8222-222222222222", "name": "Телефон", "platform": "android", "blocked": True}
other = {"id": "33333333-3333-4333-8333-333333333333", "name": "Очень длинное название телевизора в гостиной", "platform": "tv", "last_seen": "30 октября"}
link = "https://qntw.link/sub/offline-device-fixture"
session = {"cookie": "qw_session=offline-device-test", "identity": {"kind": "retail"}, "current_device": current["id"], "device_link": link,
           "account": {"subscription": {"days_left": 20, "devices": 3}}}
app.session = session
fixture = {"devices": [blocked, other, current], "paid": 3, "in_use": 3, "online_count": 1, "month_bytes": 4096,
           "link_works": True, "buy": {"price": "70 ₽", "monthly": "79 ₽ в месяц", "left": 2}}
app.render_devices(fixture)
assert app.session["devices"] == fixture["devices"]
assert app.device_list.get_row_at_index(0).device["id"] == current["id"]
assert "3 из 3" in app.devices_summary.get_text()
assert not app.device_add_button.get_sensitive()
assert app.device_buy_button.get_visible()
assert not app.device_add_button.get_visible()
assert app.device_buy_button.has_css_class("primary")
assert isinstance(app.device_ledger, Gtk.Grid)
assert any(isinstance(widget, Gtk.Label) and widget.get_text()=="Мои устройства" and
           widget.get_accessible_role()==Gtk.AccessibleRole.HEADING for widget in descendants(app.stack.get_child_by_name("devices")))
assert len(app._device_connection_actions) == 2
with patch.object(app, "show_device_connection") as show:
    app._device_connection_actions[0].emit("clicked")
    show.assert_called_once_with(current)
app._set_devices_busy(True)
assert all(not action.get_sensitive() for action in app._device_connection_actions)
app._set_devices_busy(False)
app.render_devices(dict(fixture, paid=4))
assert app.device_add_button.get_visible() and app.device_add_button.get_sensitive()
assert app.device_add_button.has_css_class("primary")
assert not app.device_buy_button.get_visible()
app.render_devices(fixture)
assert format_usage(2048) == "2.0 КБ"
assert device_presence(blocked) == "Заблокировано"
assert device_presence(other) == "Было в сети: 30 октября"

# Delete and reissue never mutate merely by opening or cancelling a dialog.
with patch.object(app, "device_request") as request:
    dialog = app.delete_account_device(other)
    request.assert_not_called()
    dialog.response(Gtk.ResponseType.CANCEL)
    request.assert_not_called()
    dialog = app.delete_account_device(other)
    dialog.response(Gtk.ResponseType.ACCEPT)
    assert request.call_args.args[1:4] == ("DELETE", f"devices/{other['id']}", None)
with patch.object(app, "device_request") as request:
    dialog = app.reissue_account_device(current)
    request.assert_not_called()
    dialog.response(Gtk.ResponseType.ACCEPT)
    assert request.call_args.args[1:4] == ("POST", f"devices/{current['id']}/reissue", {})

with patch.object(app, "device_request") as request:
    dialog = app.rename_account_device(other)
    entry = next(item for item in descendants(dialog) if isinstance(item, Gtk.Entry))
    entry.set_text("  ")
    assert not dialog.get_widget_for_response(Gtk.ResponseType.ACCEPT).get_sensitive()
    entry.set_text("ТВ в гостиной")
    dialog.response(Gtk.ResponseType.ACCEPT)
    assert request.call_args.args[1:4] == ("PATCH", f"devices/{other['id']}", {"name": "ТВ в гостиной"})

with patch.object(app, "device_request") as request:
    dialog = app.toggle_account_device(current)
    request.assert_not_called()
    dialog.response(Gtk.ResponseType.ACCEPT)
    assert request.call_args.args[1:4] == ("POST", f"devices/{current['id']}/block", {})
    app.toggle_account_device(blocked)
    assert request.call_args.args[1:4] == ("POST", f"devices/{blocked['id']}/unblock", {})

# New-device form honours the server's platforms and never submits a blank
# generic name; a selected platform may use the server's automatic name.
with patch.object(app, "device_request") as request:
    dialog = app._new_device_dialog({"platforms": [{"id": "android", "label": "Android"}]})
    assert not dialog.get_widget_for_response(Gtk.ResponseType.ACCEPT).get_sensitive()
    dropdown = next(item for item in descendants(dialog) if isinstance(item, Gtk.DropDown))
    dropdown.set_selected(1)
    assert dialog.get_widget_for_response(Gtk.ResponseType.ACCEPT).get_sensitive()
    dialog.response(Gtk.ResponseType.ACCEPT)
    assert request.call_args.args[1:4] == ("POST", "devices", {"name": "", "platform": "android"})

# Viewing a link does not activate it. Explicitly using another device needs a
# confirmation and leaves this installation's binding unchanged.
context = app.account_context()
with patch.object(app, "queue_account_subscription") as load:
    qr = "data:image/svg+xml;base64," + base64.b64encode(b'<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100"><rect width="100" height="100" fill="white"/><rect x="10" y="10" width="30" height="30" fill="black"/></svg>').decode()
    guides = [{"id": "android", "label": "Android", "apps": [{"name": "Android VPN", "guide": {"import": ["Инструкция Android"], "enable": []}}]},
              {"id": "tv", "label": "ТВ", "apps": [{"name": "Первое приложение", "guide": {"import": ["Первое подключение"], "enable": []}},
                                                       {"name": "Второе приложение", "guide": {"import": [], "enable": ["Финальная инструкция"]}}]}]
    dialog = app._show_device_connection(other, {"sub_url": link, "qr": qr, "platforms": guides})
    load.assert_not_called()
    entry = next(item for item in descendants(dialog) if isinstance(item, Gtk.Entry))
    assert not entry.get_visibility()
    picture = next(item for item in descendants(dialog) if isinstance(item, Gtk.Picture))
    assert picture.get_size_request() == (220, 220)
    instructions = next(item for item in descendants(dialog) if isinstance(item, Gtk.Expander))
    instructions.set_expanded(True)
    systems, clients = [item for item in descendants(instructions) if isinstance(item, Gtk.DropDown)]
    assert systems.get_selected() == 1  # Prefer the actual device's platform.
    assert clients.get_model().get_n_items() == 2
    clients.set_selected(1)
    assert any(isinstance(item, Gtk.Label) and "Финальная инструкция" in item.get_text() for item in descendants(instructions))
    systems.set_selected(0)
    assert clients.get_model().get_n_items() == 1
    assert any(isinstance(item, Gtk.Label) and "Инструкция Android" in item.get_text() for item in descendants(instructions))
    dialog.response(Gtk.ResponseType.CLOSE)
with patch.object(app, "on_device_connection_selected", create=True) as use:
    dialog = app._confirm_device_use(other, link, context)
    use.assert_not_called()
    dialog.response(Gtk.ResponseType.ACCEPT)
    use.assert_called_once_with(other, link, context)
    assert app.session["current_device"] == current["id"]

# A delayed old-account callback cannot replace the list or its notice.
serial = app._device_serial
before = app.status.get_text()
app.account_generation += 1
done = Mock()
app._device_request_done(context, serial, {}, "", done)
done.assert_not_called()
assert app.status.get_text() == before

# Reissue invalidates the old binding immediately and fetches the new QR only
# after refreshing the list. A foreign subscription is never imported here.
app.session = dict(session)
new = dict(current, id="44444444-4444-4444-8444-444444444444")
app.loaded_link = link
with patch.object(app, "on_account_device_changed", return_value=True, create=True) as changed, patch.object(app, "device_request") as request, patch.object(app, "show_device_connection") as show:
    app._device_changed("reissue", current, {"device": new, "message": "Новая ссылка готова."})
    assert app.session["current_device"] == new["id"]
    assert app.session["device_link"] == ""
    changed.assert_called_once()
    request.call_args.args[4](dict(fixture, devices=[new, other, blocked]))
    show.assert_called_once_with(new, activate=True)

with patch.object(app, "device_request") as request:
    dialog = app._device_place_dialog({"methods": [{"id": "card", "label": "Карта"}]})
    request.assert_not_called()
    dialog.response(Gtk.ResponseType.ACCEPT)
    assert request.call_args.args[1:4] == ("POST", "invoices", {"plan": "device", "method": "card"})

# The active account link cannot be revoked while its probe is running. A
# harmless rename can run, but its mutation lock must survive a new GET serial
# and be released even when the account callback has become stale.
app._devices_busy = False
app.active_source = {"kind": "quantway", "id": current["id"]}
app.probe_running = True
with patch("device_ui.threading.Thread") as thread:
    assert not app.device_request("Удаляем…", "DELETE", f"devices/{current['id']}", None, Mock())
    thread.assert_not_called()
    assert app.device_request("Переименовываем…", "PATCH", f"devices/{current['id']}", {"name": "Рабочий"}, Mock())
    assert app._device_mutating
    old_context, old_serial = app.account_context(), app._device_serial
    app.account_generation += 1
    app._device_serial += 1
    with patch.object(app, "load_pending_account_subscription") as pending:
        app._device_request_done(old_context, old_serial, {}, "", Mock())
        pending.assert_called_once_with()
    assert not app._device_mutating
app.probe_running = False

app.window.destroy()
stats_guard.stop()
auth_guard.stop()
print("Device UI regression passed")
