#!/usr/bin/env python3
"""Offline native GTK disclosure/menu geometry and screenshots.

Use --record-only to preserve a pre-change baseline. The harness never activates
an account mutation: menus are opened and inspected, then dismissed. Production
helpers, saved credentials and subscription workers are blocked by OfflineQuantway.
"""
import argparse
import base64
import json
from pathlib import Path
from unittest.mock import patch

import gi
gi.require_version("Gtk", "4.0")
from gi.repository import GLib, Gtk

from qa_screens import OfflineQuantway, capture, populate, settle

STATES = ("ready", "routes", "external", "account", "plans", "invoice-await", "tg-proxy",
          "devices", "support", "device-connect", "device-create", "device-buy",
          "external-rename", "external-link", "external-remove", "account-source", "telegram-link")


def descendants(widget):
    yield widget
    child = widget.get_first_child()
    while child:
        yield from descendants(child)
        child = child.get_next_sibling()


def rectangle(widget, root):
    ok, rect = widget.compute_bounds(root)
    if not ok:
        raise AssertionError("Cannot relate native widget geometry")
    return {"x": round(rect.get_x(), 2), "y": round(rect.get_y(), 2),
            "width": round(rect.get_width(), 2), "height": round(rect.get_height(), 2)}


def caption(widget):
    if isinstance(widget, Gtk.Expander):
        heading = widget.get_label_widget()
        return heading.get_text() if isinstance(heading, Gtk.Label) else widget.get_label()
    if isinstance(widget, Gtk.Button) and widget.get_label():
        return widget.get_label()
    if widget.get_css_name() == "row":
        return next((child.get_text() for child in descendants(widget) if isinstance(child, Gtk.Label)), "row")
    return widget.get_tooltip_text() or widget.get_css_name()


def title_widget(expander):
    # GTK4's documented nodes: expander-widget > box > title; the title owns
    # the arrow and label, while the disclosure's body is its next sibling.
    return next(widget for widget in descendants(expander) if widget.get_css_name() == "title")


def reveal(widget):
    """Move the nearest real viewport to expose a header or menu anchor."""
    ancestor = widget.get_parent()
    while ancestor and not isinstance(ancestor, Gtk.ScrolledWindow):
        ancestor = ancestor.get_parent()
    if ancestor:
        box = rectangle(widget, ancestor)
        adjustment = ancestor.get_vadjustment()
        desired = adjustment.get_value() + box["y"] - 16
        adjustment.set_value(max(adjustment.get_lower(), min(desired, adjustment.get_upper() - adjustment.get_page_size())))
        settle(50)


def disclosure_geometry(expander, root):
    header, body = title_widget(expander), expander.get_child()
    h, b = rectangle(header, root), rectangle(body, root)
    actions = [widget for widget in descendants(body)
               if isinstance(widget, (Gtk.Button, Gtk.DropDown, Gtk.Entry)) and widget.get_mapped()]
    return {"label": caption(expander), "header": h, "body": b,
            "gap": round(b["y"] - h["y"] - h["height"], 2),
            "actions": [{"caption": caption(widget), "bounds": rectangle(widget, root)} for widget in actions]}


def prepare(app, state):
    if state == "account-source":
        populate(app, "account")
        app.session["device_link"] = "https://example.invalid/bound-quantway-source"
        app.active_source = {"kind": "quantway", "id": app.session["current_device"], "name": "Quantway", "link": app.session["device_link"]}
        app.render_account(app.session)
        app.sync_controls()
        assert not app.account_use_button.get_visible()
        assert app.renew_button.has_css_class("primary")
        app.active_source = {"kind": "external", "id": "external-fixture", "name": "Другой провайдер", "link": "https://example.invalid/external"}
        app.sync_controls()
        return
    if state in ("external-rename", "external-link", "external-remove"):
        populate(app, "external")
        methods = {"external-rename": app.rename_external_subscription,
                   "external-link": app.edit_external_link, "external-remove": app.remove_external_subscription}
        popover = app._external_rows[1]["menu"].get_popover()
        methods[state](popover, app.external_subscriptions[1]["id"])
        windows = Gtk.Window.get_toplevels()
        app.qa_capture_window = next(windows.get_item(index) for index in range(windows.get_n_items())
                                    if isinstance(windows.get_item(index), Gtk.Dialog) and
                                    windows.get_item(index).get_transient_for() is app.window)
        return
    if state == "routes":
        populate(app, "ready")
        app.nav_buttons[1].set_active(True)
        return
    if state == "device-buy":
        populate(app, "devices")
        app.qa_capture_window = app._device_place_dialog({"methods": [{"id": "card", "label": "Банковская карта"}, {"id": "btc", "label": "Bitcoin"}]})
        return
    populate(app, state)
    if state == "invoice-await":
        app.show_account_invoice({"invoice": {"id": "11111111-2222-4333-8444-555555555555",
            "title": "Подписка на месяц", "is_fiat": False, "amount_btc": "0.00004421",
            "state": "awaiting", "status": "Ожидает оплаты", "address": "bc1qofflinefixtureaddressnotarealwalletforvisualqa",
            "date": "31 октября 2026", "pay_url": "https://pay.example.invalid/offline-fixture"}})
    elif state == "device-connect":
        app.qa_capture_window.destroy()
        svg = '<svg xmlns="http://www.w3.org/2000/svg" width="220" height="220"><rect width="220" height="220" fill="white"/><rect x="25" y="25" width="65" height="65" fill="black"/></svg>'
        guide = {"import": ["Установите приложение на это устройство.", "Вставьте полную ссылку на подписку и загрузите маршруты."],
                 "enable": ["Нажмите кнопку подключения и подтвердите запрос системы."]}
        app.qa_capture_window = app._show_device_connection({"id": "11111111-2222-4333-8444-555555555555", "name": "Телевизор в гостиной", "platform": "tv"},
            {"sub_url": "https://qntw.link/sub/offline-fixture", "qr": "data:image/svg+xml;base64," + base64.b64encode(svg.encode()).decode(),
             "platforms": [{"id": "tv", "label": "Android TV", "how": "Управляйте приложением с пульта телевизора.",
                            "apps": [{"name": "Quantway", "guide": guide, "install_url": "https://example.invalid/install"},
                                     {"name": "Другое приложение", "guide": guide}]},
                           {"id": "android", "label": "Android", "apps": [{"name": "Quantway", "guide": guide}]}]})


def close_app(app, target):
    app.stop_support_poll()
    if app.connection_timer is not None:
        GLib.source_remove(app.connection_timer)
        app.connection_timer = None
    app.vpn_process = None
    if target is not app.window:
        target.destroy()
    app.window.destroy()
    app.quit()
    settle(20)


def inspect_state(state, output, width, height, scale, record_only):
    app = OfflineQuantway()
    app.register(None)
    app.activate()
    app.window.set_default_size(width, height)
    app.stack.set_transition_type(Gtk.StackTransitionType.NONE)
    with patch("quantway.threading.Thread"):
        prepare(app, state)
    app.window.present()
    settle()
    root = getattr(app, "qa_capture_window", None) or app.window
    expanders = [widget for widget in descendants(root) if isinstance(widget, Gtk.Expander) and widget.get_visible()]
    for expander in expanders:
        # Hidden Gtk.Stack pages are still descendants; only inspect the page
        # or modal currently on screen.
        if expander.get_mapped():
            expander.set_expanded(True)
    settle(80)
    records = []
    sequence = 0
    def snapshot(kind, details, target=root):
        nonlocal sequence
        sequence += 1
        name = f"{state}-{sequence}-{kind}-{width}x{height}-scale{scale:g}.png"
        capture(target, output / name, "gtk")
        records.append({"state": state, "kind": kind, "file": name,
                        "window": [app.window.get_width(), app.window.get_height()],
                        "capture": [target.get_width(), target.get_height()], **details})
    try:
        assert app.window.get_width() <= width, (state, "window grew", app.window.get_width(), width)
        if state == "ready":
            geometry = {"connect": rectangle(app.connect_button, root), "probe": rectangle(app.probe_button, root),
                        "routes": rectangle(app.home_columns, root)}
            snapshot("overview", geometry)
            if not record_only:
                assert app.probe_button.is_ancestor(app.connection_settings_actions)
                assert app.connect_button.is_ancestor(app.connection_actions)
                assert app.connection_actions.get_parent() is app.connection_panel
                assert app.connection_actions.has_css_class("connection-footer")
                assert geometry["connect"]["y"] + geometry["connect"]["height"] <= root.get_height(), geometry
                assert geometry["probe"]["y"] + geometry["probe"]["height"] <= geometry["routes"]["y"], geometry
        if state == "external":
            actions = [row["use"] for row in app._external_rows]
            geometry = {"actions": [{"caption": caption(widget), "bounds": rectangle(widget, root),
                                     "lines": widget.get_child().get_layout().get_line_count()} for widget in actions]}
            snapshot("overview", geometry)
            if not record_only:
                assert all(action["lines"] == 1 for action in geometry["actions"]), ("short CTA split within a word", geometry)
        if state == "routes":
            geometry = {"probe": rectangle(app.routes_probe_button, root), "header": rectangle(app.routes_header, root)}
            snapshot("overview", geometry)
            if not record_only:
                assert app.routes_probe_button.is_ancestor(app.routes_header)
                assert geometry["probe"]["x"] + geometry["probe"]["width"] <= root.get_width(), geometry
        if state == "devices":
            stats = app.device_ledger
            assert stats.get_mapped() and stats.has_css_class("device-ledger")
            geometry = {"buy": rectangle(app.device_buy_button, root), "stats": rectangle(stats, root)}
            snapshot("overview", geometry)
            if not record_only:
                assert app.device_buy_button.is_ancestor(app.device_actions)
                assert geometry["buy"]["y"] + geometry["buy"]["height"] <= geometry["stats"]["y"], geometry
        if state == "account-source":
            use, renew = app.account_use_button, app.renew_button
            snapshot("overview", {"use": rectangle(use, root), "renew": rectangle(renew, root)})
            if not record_only:
                assert use.get_parent() is app.account_actions
                assert use.get_visible() and use.has_css_class("primary")
                assert not renew.has_css_class("primary")
                assert rectangle(use, root)["y"] + rectangle(use, root)["height"] <= rectangle(renew, root)["y"]
                app.active_source = {"kind": "quantway", "id": app.session["current_device"]}
                app.sync_controls()
                assert not use.get_visible() and renew.has_css_class("primary")
                app.active_source = {"kind": "external", "id": "external-fixture"}
                app.render_account(app.session)
                assert sum(isinstance(widget, Gtk.Button) and widget.get_label() == "Использовать подписку Quantway"
                           for widget in descendants(app.account_actions)) == 1
        if state == "invoice-await":
            summary = next(widget for widget in descendants(root) if widget.get_mapped() and widget.has_css_class("invoice-summary"))
            pay = next(widget for widget in descendants(summary) if isinstance(widget, Gtk.Button) and widget.get_label() == "Перейти к оплате")
            snapshot("overview", {"pay": rectangle(pay, root), "check": rectangle(app.invoice_check_button, root)})
            if not record_only:
                assert pay.is_ancestor(summary) and app.invoice_check_button.is_ancestor(summary)
                assert pay.get_parent().get_parent() is app.invoice_check_button.get_parent().get_parent()
        if state == "telegram-link":
            actions = [widget for widget in descendants(root) if isinstance(widget, Gtk.Button) and widget.get_mapped() and
                       widget.get_label() in ("Открыть Telegram", "Скопировать ссылку")]
            snapshot("overview", {"actions": [{"caption": caption(widget), "bounds": rectangle(widget, root)} for widget in actions]})
            if not record_only:
                assert len(actions) == 2 and isinstance(actions[0].get_parent().get_parent(), Gtk.FlowBox)
                assert actions[0].get_parent().get_parent() is actions[1].get_parent().get_parent()
                assert actions[0].has_css_class("primary") and not actions[1].has_css_class("primary")
        if state.startswith("external-"):
            accept = root.get_widget_for_response(Gtk.ResponseType.ACCEPT)
            cancel = root.get_widget_for_response(Gtk.ResponseType.CANCEL)
            geometry = {"accept": rectangle(accept, root), "cancel": rectangle(cancel, root)}
            snapshot("dialog", geometry)
            if not record_only:
                assert root.has_css_class("quantway")
                assert root.get_content_area().has_css_class("workspace")
                assert accept.has_css_class("danger-action" if state == "external-remove" else "primary")
                assert geometry["cancel"]["x"] + geometry["cancel"]["width"] + 7 <= geometry["accept"]["x"], geometry
                assert geometry["accept"]["x"] + geometry["accept"]["width"] <= root.get_width(), geometry
                if state == "external-remove":
                    assert root.get_default_widget() is cancel
        for expander in expanders:
            if not expander.get_mapped():
                continue
            reveal(title_widget(expander))
            geometry = disclosure_geometry(expander, root)
            snapshot("disclosure", geometry)
            if not record_only:
                validate_disclosure(geometry)
                assert expander.grab_focus(), (state, "disclosure cannot receive keyboard focus")
                assert expander.activate(), (state, "no native activation action")
                settle(30)
                assert not expander.get_expanded(), (state, "native keyboard activation did not collapse")
                assert not expander.get_child().get_mapped(), (state, "collapsed content remains mapped")
                focused = root.get_focus()
                assert focused and not focused.is_ancestor(expander.get_child()), (state, "focus trapped in hidden body")
                assert expander.activate()
                settle(30)
                assert expander.get_expanded()
                if state == "device-connect":
                    install = next(widget for widget in descendants(expander.get_child())
                                   if isinstance(widget, Gtk.Button) and widget.get_label() == "Установить приложение")
                    notes = [widget for widget in descendants(expander.get_child()) if isinstance(widget, Gtk.Label) and
                             (widget.get_text().startswith("1.") or "Управляйте приложением" in widget.get_text())]
                    assert notes
                    assert rectangle(install, root)["y"] + rectangle(install, root)["height"] <= min(rectangle(note, root)["y"] for note in notes)
        # Open every menu in the active page, rather than only checking their
        # closed icon bounds. No menu action is invoked.
        menus = [widget for widget in descendants(root) if isinstance(widget, Gtk.MenuButton) and widget.get_mapped()]
        for menu in menus:
            reveal(menu)
            menu.popup()
            settle(60)
            popover = menu.get_popover()
            if popover and popover.get_mapped():
                geometry = popup_geometry(popover)
                snapshot("menu", {"caption": caption(menu), **geometry}, popover)
                if not record_only:
                    validate_popup(geometry)
            menu.popdown()
            settle(30)
        dropdowns = [widget for widget in descendants(root) if isinstance(widget, Gtk.DropDown) and widget.get_mapped()]
        for dropdown in dropdowns:
            reveal(dropdown)
            toggle = next((widget for widget in descendants(dropdown) if isinstance(widget, Gtk.ToggleButton)), None)
            if toggle:
                toggle.set_active(True)
            else:
                dropdown.activate()
            settle(60)
            popover = next((widget for widget in descendants(dropdown) if isinstance(widget, Gtk.Popover) and widget.get_mapped()), None)
            if popover:
                geometry = popup_geometry(popover)
                snapshot("dropdown", geometry, popover)
                if not record_only:
                    validate_popup(geometry)
                popover.popdown()
            if toggle:
                toggle.set_active(False)
            settle(30)
        return records
    finally:
        close_app(app, root)


def popup_geometry(popover):
    controls = [widget for widget in descendants(popover) if (isinstance(widget, (Gtk.Button, Gtk.ListBoxRow)) or widget.get_css_name() == "row") and widget.get_mapped()]
    def in_scroll(widget):
        parent = widget.get_parent()
        while parent and parent is not popover:
            if isinstance(parent, Gtk.ScrolledWindow):
                return True
            parent = parent.get_parent()
        return False
    return {"bounds": {"width": popover.get_width(), "height": popover.get_height()},
            "actions": [{"caption": caption(widget), "bounds": rectangle(widget, popover),
                         "scrollable": in_scroll(widget)} for widget in controls]}


def validate_disclosure(geometry):
    assert geometry["header"]["height"] >= 32, geometry
    assert geometry["gap"] >= 8, geometry
    for action in geometry["actions"]:
        bounds = action["bounds"]
        assert bounds["y"] >= geometry["header"]["y"] + geometry["header"]["height"], geometry


def validate_popup(geometry):
    # Non-scrolling popup controls fit wholly. Scroll-backed history/list rows
    # may legitimately extend vertically beyond the popup's viewport.
    assert geometry["actions"], ("popup has no allocated actions", geometry)
    assert geometry["bounds"]["width"] >= 160, ("popup collapsed to word fragments", geometry)
    for index, action in enumerate(geometry["actions"]):
        bounds = action["bounds"]
        assert bounds["x"] >= -1, geometry
        assert bounds["x"] + bounds["width"] <= geometry["bounds"]["width"] + 1, geometry
        if not action["scrollable"]:
            assert bounds["y"] >= -1, geometry
            assert bounds["y"] + bounds["height"] <= geometry["bounds"]["height"] + 1, geometry
        for other in geometry["actions"][index+1:]:
            other = other["bounds"]
            overlap_x = min(bounds["x"] + bounds["width"], other["x"] + other["width"]) - max(bounds["x"], other["x"])
            overlap_y = min(bounds["y"] + bounds["height"], other["y"] + other["height"]) - max(bounds["y"], other["y"])
            assert overlap_x <= 1 or overlap_y <= 1, ("popup action overlap", geometry)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--width", type=int, default=1000)
    parser.add_argument("--height", type=int, default=740)
    parser.add_argument("--scale", type=float, default=1)
    parser.add_argument("--states", nargs="+", choices=STATES, default=STATES)
    parser.add_argument("--record-only", action="store_true")
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    if not Gtk.init_check():
        raise RuntimeError("A native GTK display is required")
    Gtk.Settings.get_default().set_property("gtk-xft-dpi", int(96 * 1024 * args.scale))
    Gtk.Settings.get_default().set_property("gtk-enable-animations", False)
    records = []
    for state in args.states:
        records.extend(inspect_state(state, args.output, args.width, args.height, args.scale, args.record_only))
        (args.output / "manifest.json").write_text(json.dumps(records, ensure_ascii=False, indent=2) + "\n")
    (args.output / "manifest.json").write_text(json.dumps(records, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps({"passed": not args.record_only, "recorded": len(records), "output": str(args.output)}, ensure_ascii=False))


if __name__ == "__main__":
    main()
