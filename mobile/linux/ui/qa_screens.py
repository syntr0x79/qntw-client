#!/usr/bin/env python3
"""Render Quantway's real GTK widgets with deterministic, offline fixture data.

Example inside a disposable Linux desktop/Xvfb session:
  GSK_RENDERER=cairo python3 qa_screens.py --output /tmp/quantway-ui --width 1000 --height 740

Ubuntu packages: python3-gi gir1.2-gtk-4.0 xvfb fonts-dejavu-core
fonts-noto-color-emoji adwaita-icon-theme imagemagick. Headless invocation:
  xvfb-run -a -s '-screen 0 2000x1600x24' env GSK_RENDERER=cairo python3 qa_screens.py

Requires PyGObject/GTK 4; ImageMagick's `import` is the optional X11 fallback.
No helper, network, account mutation, or real VPN process is used.
The output JSON manifest records the actual window size and capture backend.
Cabinet states include invoices, invoice-await/paid, support/support-form,
ticket, referral, tg-proxy, token and history. Optional modal states are
device-connect/create/rename/delete; all URLs and QR artwork are offline fixtures.
"""
from __future__ import annotations

import argparse
import base64
import json
from pathlib import Path
import shutil
import subprocess
import time
import uuid
from datetime import datetime, timedelta, timezone
from unittest.mock import patch

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Gdk", "4.0")
gi.require_version("Gsk", "4.0")
from gi.repository import Gio, GLib, Gtk, Gsk

from quantway import Quantway

CORE_STATES = ["welcome", "external", "checking", "ready", "ready-long", "connected", "routes", "route-error", "account-login", "account", "devices", "plans", "replacement"]
CABINET_STATES = ["invoices", "invoices-empty", "invoices-error", "invoice-await", "invoice-paid", "support", "support-form", "support-error", "ticket", "referral", "tg-proxy", "proxy-empty", "token", "telegram-link", "history", "history-empty", "plans-error", "devices-empty"]
MODAL_STATES = ["device-connect", "device-create", "device-rename", "device-delete"]
TINC_STATES = ["tinc-sidebar", "tinc-ready", "tinc-waiting", "tinc-connected", "tinc-home-connected", "tinc-mesh-partial", "tinc-mesh-stale", "tinc-disconnected", "tinc-expired", "tinc-unavailable", "tinc-country-confirm"]


class OfflineQuantway(Quantway):
    """Use production widgets and render callbacks, never production I/O."""

    def __init__(self):
        super().__init__()
        self.set_application_id("link.quantway.visualqa.c" + uuid.uuid4().hex)
        self.set_flags(Gio.ApplicationFlags.NON_UNIQUE)

    def call_helper(self, *_args, **_kwargs):
        raise AssertionError("The visual QA harness must never call a helper")

    def refresh_account_stats(self):
        self.render_account_stats()

    def start_session_restore(self):
        pass

    def forget_saved_account(self):
        pass

    def persist_account_session(self, *_args, **_kwargs):
        pass

    def load(self, *_args):
        raise AssertionError("The visual QA harness must never load a subscription")

    def start_probe(self, *_args):
        raise AssertionError("The visual QA harness must never run probes")

    def toggle_vpn(self, *_args):
        raise AssertionError("The visual QA harness must never start or stop a VPN")


class FixtureProcess:
    stdin = None

    @staticmethod
    def poll():
        return 0

    @staticmethod
    def terminate():
        pass


def profiles():
    return [
        {"job_id": "fixture-de", "label": "🇩🇪 Германия · Франкфурт", "engine": "xray", "protocol": "VLESS", "transport": "XHTTP"},
        {"job_id": "fixture-nl", "label": "🇳🇱 Нидерланды · Амстердам — очень длинное название альтернативного маршрута для проверки переполнения", "engine": "amneziawg", "protocol": "AWG", "transport": ""},
        {"job_id": "fixture-pl", "label": "🇵🇱 Польша · Варшава", "engine": "xray", "protocol": "HY2", "transport": ""},
        {"job_id": "fixture-fr", "label": "🇫🇷 Франция · Париж", "engine": "xray", "protocol": "VLESS", "transport": "TCP"},
        {"job_id": "fixture-us", "label": "🇺🇸 США · Нью-Йорк", "engine": "xray", "protocol": "VLESS", "transport": "XHTTP"},
    ]


def ready(app):
    data = profiles()
    app.entry.set_text("https://example.invalid/visual-qa")
    app.loaded_link = app.entry.get_text()
    app.show_result(data, "")
    results = []
    for index, profile in enumerate(data):
        results.append({"job_id": profile["job_id"], "outcome": "ok" if index < 4 else "fail", "elapsed_ms": 22 + index * 8,
                        "job": {"engine": profile["engine"]} if index < 4 else None,
                        "control_url": "https://example.invalid/health"})
    app.show_probe(results, "")
    app.add_subscription_button.set_visible(False)
    app.connect_button.set_visible(True)


def set_page(app, page):
    names = {"home": 0, "routes": 1, "account": 2, "devices": 2, "external": 3}
    app.nav_buttons[names[page]].set_active(True)
    app.stack.set_visible_child_name(page)


def populate(app, state):
    if state in TINC_STATES:
        link = "https://example.invalid/current-device-offline-fixture"
        app.session = {"cookie": "qw_session=visual-tinc-fixture", "current_device": "this-computer", "device_link": link}
        app.active_source = {"kind": "quantway", "id": "this-computer", "name": "Quantway", "link": link}
        app.entry.set_text(link)
        app.loaded_link = link
        app.sync_controls()
        app.open_tinc_page()
        data = {"schema_version": 1, "state": "available", "requested_country": "PL",
                "countries": [{"code": "PL", "state": "available"}, {"code": "SE", "state": "available"}]}
        error = "Доступ к Tinc для этого устройства ограничен. Обратитесь в поддержку." if state == "tinc-unavailable" else ""
        app.tinc_catalog_received(data, error, app.tinc_source_context(), app.tinc_catalog_serial)
        app.apply_tinc_mode("tinc")
        if state in ("tinc-waiting", "tinc-connected", "tinc-home-connected", "tinc-mesh-partial", "tinc-mesh-stale", "tinc-expired", "tinc-country-confirm"):
            app.tinc_worker_active = True
            app.vpn_process = FixtureProcess()
            names = ["in-ge", "in-moscow-fast", "in-moscow-hy", "in-moscow-vless", "in-moscow-wg", "in-nl", "in-pl", "in-ru-tg", "in-ru-yandex", "in-se", "in-us"]
            now = datetime.now(timezone.utc)
            app._tinc_traffic_sample = ("offline-one", now.timestamp() - 2, 26 * 1024**2, 3 * 1024**2 - 32 * 1024)
            stamp = (now - timedelta(seconds=30) if state == "tinc-mesh-stale" else now).isoformat()
            connected = names[:-2] if state == "tinc-mesh-partial" else names
            event_state = "waiting" if state == "tinc-waiting" else "expired" if state == "tinc-expired" else "connected"
            app.tinc_vpn_event({"status": event_state,
                                "country": "PL", "desired_country": "PL", "connected_ingresses": connected,
                                "mesh": {"observation_state": "ok", "observed_at": stamp,
                                         "ingresses": [{"name": name, "connected": name in connected} for name in names],
                                         "egress": {"country": "PL", "id": "e87f3ca3-b9b7-4d98-90ed-0d07ce9f1427"},
                                         "traffic": {"source": "system_tun", "epoch": "offline-one", "sampled_at": stamp,
                                                     "rx_bytes": 28 * 1024**2, "tx_bytes": 3 * 1024**2}}},
                               app.vpn_generation, app.tinc_source_context())
        if state not in ("tinc-sidebar", "tinc-connected", "tinc-mesh-partial", "tinc-mesh-stale"):
            set_page(app, "home")
        if state == "tinc-country-confirm":
            app.confirm_tinc_change("country", "SE")
            app.qa_capture_window = app.tinc_change_dialog
        return
    if state in ("empty", "welcome"):
        return
    if state == "external":
        app.external_subscriptions = [
            {"id": "fixture-work", "name": "Рабочий VPN", "link": "https://provider.example.invalid/sub?fixture-token=one"},
            {"id": "fixture-personal", "name": "Личная подписка с очень длинным названием для проверки строки провайдера", "link": "https://vpn.example.invalid/sub?fixture-token=two"},
        ]
        app.active_source = dict(app.external_subscriptions[0], kind="external")
        app.render_external_subscriptions()
        app.external_expander.set_expanded(False)
        set_page(app, "external")
        app.status.set_text("")
        return
    if state in ("ready", "ready-long", "connected", "routes", "checking", "route-error"):
        ready(app)
        if state == "ready-long":
            app.choose_best(None, app.current_profiles[1])
        elif state == "checking":
            # Run the real transition while replacing its background thread;
            # this keeps state copy/styles accurate without invoking a probe.
            with patch("quantway.threading.Thread"):
                Quantway.start_probe(app, None)
            app.show_progress(14, 56)
            if hasattr(app, "sync_controls"):
                app.sync_controls()
            app.status.set_text("Проверяем доступные маршруты в текущей сети.")
        elif state == "connected":
            app.vpn_process = FixtureProcess()
            app.vpn_connected()
            app.connected_since = time.monotonic() - 754
            app.update_connection_time()
        elif state == "routes":
            set_page(app, "routes")
        elif state == "route-error":
            app.show_probe([], "Не удалось проверить маршруты. Проверьте сеть и попробуйте снова.")
        return
    set_page(app, "account")
    if state == "account-login":
        return
    session = {
        "cookie": "qw_session=visual-qa-fixture", "identity": {"kind": "retail"},
        "account": {"subscription": {"headline": "Подписка Quantway", "status": "active", "days_left": 24, "until_text": "Активна до 24 ноября", "devices": 3},
                    "features": {"support": True, "images": True, "referral": True, "promo": True}},
        "devices": [{"id": "11111111-2222-4333-8444-555555555555", "name": "Телефон с длинным названием для проверки переноса и обрезки", "platform": "android"}],
        "current_device": "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee",
    }
    if state == "replacement":
        session["current_device"] = None
        session["needs_replacement"] = True
        session["device_error"] = "Нет свободных мест. Выберите устройство для замены."
    app.show_devices(session, "")
    app.update_account_stats({"month_bytes": 19200000000, "in_use": 3, "paid": 7, "online_count": 1,
                              "devices": ([{"id": session["current_device"], "month_bytes": 8200000000}] if session.get("current_device") else []) +
                                         [{"id": "11111111-2222-4333-8444-555555555555", "month_bytes": 11000000000},
                                          {"id": "99999999-2222-4333-8444-555555555555", "month_bytes": 0}]})
    if state in ("devices", "devices-empty"):
        app.render_devices({"paid": 3, "in_use": 3, "online_count": 1, "month_bytes": 19200000000,
                            "link_works": True, "buy": {"price": "70 ₽", "left": 2, "monthly": "79 ₽ в месяц"},
                            "devices": [
                                {"id": session["current_device"], "name": "Рабочий компьютер", "platform": "linux", "online": True, "month_bytes": 8200000000},
                                {"id": "11111111-2222-4333-8444-555555555555", "name": "Телефон с длинным названием для проверки переноса и обрезки", "platform": "android", "last_seen": "Сегодня, 12:40", "month_bytes": 11000000000},
                                {"id": "99999999-2222-4333-8444-555555555555", "name": "Телевизор", "platform": "tv", "blocked": True},
                            ]})
        if state == "devices-empty":
            app.render_devices({"paid": 3, "in_use": 0, "online_count": 0, "month_bytes": 0,
                                "link_works": True, "devices": []})
        set_page(app, "devices")
        return
    if state in ("plans", "plans-error"):
        if state == "plans-error":
            app.load_plans(None)
            app.show_plans({}, "Не удалось загрузить тарифы. Попробуйте ещё раз.", app.account_context(renewal=True))
            return
        app.show_plans({"plans": [
            {"id": "month", "title": "На месяц", "price": "299 ₽", "days": 30, "devices": 3,
             "totals": [{"devices": 1, "price": "378 ₽"}]},
            {"id": "year", "title": "На год", "price": "2 499 ₽", "days": 365, "devices": 3}],
            "methods": [{"id": "card", "label": "Банковская карта"}, {"id": "btc", "label": "Bitcoin"}],
            "devices": {"current": 1, "max": 5}}, "")
    elif state in CABINET_STATES:
        app.stack.set_visible_child_name("cabinet-section")
        app.status.set_text("")
        invoice = {"id": "11111111-2222-4333-8444-555555555555", "title": "Подписка на месяц", "is_fiat": True,
                   "amount_rub": "299 ₽", "state": "awaiting", "status": "Ожидает оплаты", "date": "31 октября 2026",
                   "deadline": "31 октября, 15:30", "pay_url": "https://pay.example.invalid/offline-fixture"}
        messages = [
            {"from_us": False, "author": "Вы", "at": "Сегодня, 10:40", "body": "На домашнем Wi-Fi VPN подключается, но часть сайтов не открывается. На мобильной сети всё работает."},
            {"from_us": True, "author": "Поддержка Quantway", "at": "Сегодня, 10:44", "body": "Отключите VPN и повторите проверку маршрутов в домашней сети. После проверки приложение выберет доступный маршрут. Если проблема останется, пришлите снимок ошибки.", "images": [77]},
            {"from_us": False, "author": "Вы", "at": "Сегодня, 10:47", "body": "Проверил ещё раз. Германия работает, спасибо."},
        ]
        if state == "invoices":
            app.render_cabinet_section("invoices", {"invoices": [invoice,
                dict(invoice, id="22222222-2222-4333-8444-555555555555", title="Подписка на год", amount_rub="2 499 ₽", state="paid", status="Оплачен", date="12 сентября 2026"),
                dict(invoice, id="33333333-2222-4333-8444-555555555555", title="Дополнительное устройство", amount_rub="79 ₽", state="expired", status="Срок оплаты истёк", date="4 сентября 2026")],
                "more": True, "next_before": "2026-09-04T12:00:00Z"})
        elif state == "invoices-empty":
            app.render_cabinet_section("invoices", {"invoices": []})
        elif state == "invoices-error":
            app.open_cabinet_section("invoices")
            app.cabinet_response(app.account_context(), app.cabinet_serial, {}, "Не удалось загрузить счета. Проверьте сеть и повторите попытку.", None, "cabinet-section")
        elif state in ("invoice-await", "invoice-paid"):
            app.show_account_invoice({"invoice": dict(invoice, state="paid", status="Оплата получена") if state == "invoice-paid" else invoice})
        elif state in ("support", "support-form"):
            data = {"max_runes": 4000,
                "current": {"id": 73, "state": "Мы ответили. Если вопрос остался, напишите здесь же.", "messages": messages} if state == "support" else None,
                "telegram": True,
                "history": [{"id": 42, "subject": "Подключение на телевизоре в гостиной", "closed": "24 октября 2026"}]}
            app.open_support_chat()
            app.support_response(app.account_context(), app._support_serial, data, "", app.render_support_chat)
        elif state == "support-error":
            app.open_support_chat()
            app.support_response(app.account_context(), app._support_serial, {}, "Не удалось связаться с поддержкой. Попробуйте ещё раз.", None)
        elif state == "ticket":
            data = {"ticket": {"id": 42, "subject": "Подключение на телевизоре в гостиной", "closed": True}, "messages": messages}
            app.open_support_chat(42)
            app.support_response(app.account_context(), app._support_serial, data, "", lambda result: app.render_support_chat(result, 42))
        elif state == "referral":
            app.render_cabinet_section("referral", {"invited": 12, "paid": 8, "days_earned": 84,
                "per_invite": 7, "friend_days": 3, "cap": 180, "left": 96,
                "share_text": "Попробуйте Quantway: быстрый доступ к нужным ресурсам. По моей ссылке вы получите бонус при подключении.",
                "link": "https://qntw.link/friend/offline-fixture"})
        elif state == "tg-proxy":
            app.render_cabinet_section("tg-proxy", {"state": "ready", "until_text": "Прокси активен до 24 ноября 2026",
                "links": [{"host": "Германия · Франкфурт", "url": "https://t.me/proxy?server=offline.example.invalid&port=443&secret=fixture"},
                          {"host": "Нидерланды · Амстердам", "url": "https://t.me/proxy?server=offline2.example.invalid&port=443&secret=fixture"}]})
        elif state == "proxy-empty":
            app.render_cabinet_section("tg-proxy", {"state": "unpaid", "until_text": "Продлите подписку для подключения прокси.", "links": []})
        elif state == "history":
            app.render_cabinet_section("history", {"moves": [{"delta": 30, "what": "Продление подписки", "when": "25 октября 2026"},
                                                                      {"delta": 7, "what": "Бонус за приглашение", "when": "12 октября 2026"}]})
        elif state == "token":
            app.show_new_token({"code": "7c06fd57f3bd4d60a47b656b832ca03f", "session_cookie": "qw_session=new-offline-fixture"})
        elif state == "history-empty":
            app.render_cabinet_section("history", {"moves": []})
        elif state == "telegram-link":
            app.show_telegram_link({"url": "https://t.me/qntw_bot?start=offline-link-fixture"})
    elif state in ("device-connect", "device-create", "device-rename", "device-delete"):
        device = {"id": "11111111-2222-4333-8444-555555555555", "name": "Телевизор в гостиной", "platform": "tv"}
        app.render_devices({"paid": 3, "in_use": 2, "online_count": 1, "link_works": True, "devices": [device]})
        set_page(app, "devices")
        if state == "device-connect":
            # A decorative offline QR fixture tests image/layout handling; it
            # contains no account key and is never used to connect a device.
            svg = '<svg xmlns="http://www.w3.org/2000/svg" width="220" height="220" viewBox="0 0 29 29"><rect width="29" height="29" fill="white"/>'
            svg += ''.join(f'<rect x="{x+2}" y="{y+2}" width="1" height="1"/>' for x in range(25) for y in range(25) if (x*x+y+3*x*y) % 5 < 2)
            svg += '</svg>'
            app.qa_capture_window = app._show_device_connection(device, {"sub_url": "https://qntw.link/sub/offline-fixture",
                "qr": "data:image/svg+xml;base64," + base64.b64encode(svg.encode()).decode(),
                "platforms": [{"label": "Android TV", "how": "Установите приложение на телевизор и добавьте ссылку на подписку."}]})
        elif state == "device-create":
            app.qa_capture_window = app._new_device_dialog({"platforms": [{"id": "android", "label": "Android"}, {"id": "tv", "label": "Android TV"}, {"id": "windows", "label": "Windows"}]})
        elif state == "device-rename":
            app.qa_capture_window = app.rename_account_device(device)
        else:
            app.qa_capture_window = app.delete_account_device(device)


def settle(milliseconds=300):
    deadline = time.monotonic() + milliseconds / 1000
    context = GLib.MainContext.default()
    while time.monotonic() < deadline:
        while context.pending():
            context.iteration(False)
        time.sleep(0.01)


def scroll_to_bottom(widget):
    """Expose lower content for a second QA capture without changing the UI."""
    if isinstance(widget, Gtk.ScrolledWindow):
        adjustment = widget.get_vadjustment()
        adjustment.set_value(max(adjustment.get_lower(), adjustment.get_upper() - adjustment.get_page_size()))
        return
    child = widget.get_first_child()
    while child:
        if child.get_visible():
            scroll_to_bottom(child)
        child = child.get_next_sibling()


def capture_gtk(window, path):
    """Native widget snapshot; availability varies with distro GI bindings."""
    paintable = Gtk.WidgetPaintable.new(window)
    snapshot = Gtk.Snapshot()
    paintable.snapshot(snapshot, window.get_width(), window.get_height())
    node = snapshot.to_node()
    if node is None:
        raise RuntimeError("GTK returned an empty render node")
    renderer = Gsk.Renderer.new_for_surface(window.get_surface())
    try:
        texture = renderer.render_texture(node, None)
        if not texture.save_to_png(str(path)):
            raise RuntimeError("GTK failed to save PNG")
    finally:
        renderer.unrealize()


def capture(window, path, backend):
    errors = []
    if backend in ("auto", "gtk"):
        try:
            capture_gtk(window, path)
            return "gtk"
        except Exception as exc:
            errors.append(f"GTK: {type(exc).__name__}: {exc}")
            if backend == "gtk":
                raise RuntimeError("; ".join(errors)) from exc
    if not shutil.which("import"):
        raise RuntimeError("; ".join(errors + ["ImageMagick import unavailable; install imagemagick for X11 fallback"]))
    # The harness must run in its own Xvfb desktop. Root capture is reliable
    # across GTK/GSK releases and includes the complete allocated window.
    subprocess.run(["import", "-window", "root", str(path)], check=True, timeout=15,
                   stdout=subprocess.DEVNULL, stderr=subprocess.PIPE)
    return "x11-root"


def layout_measurements(app):
    """Record allocation constraints to explain any window-size overflow."""
    widgets = {name: getattr(app, name) for name in (
        "sidebar", "stack", "home_heading", "home_columns", "progress_box",
        "connection_title", "connection_hint", "account_actions", "tinc_controls", "tinc_country_picker", "tinc_page_state",
        "tinc_mesh_panel", "tinc_mesh_path", "tinc_mesh_nodes")}
    widgets["summary_heading"] = app.summary_title.get_parent()
    widgets["connection_actions"] = app.connect_button.get_parent()
    widgets["probe_actions"] = app.probe_button.get_parent()
    for name in ("support_scroll", "support_composer", "support_editor", "support_composer_actions", "account_card_columns", "account_stats_panel"):
        if hasattr(app, name):
            widgets[name] = getattr(app, name)
    def measure(widget):
        minimum, natural = widget.measure(Gtk.Orientation.HORIZONTAL, -1)[:2]
        return {"minimum": minimum, "natural": natural, "allocated": widget.get_width(),
                "minimum_height": widget.measure(Gtk.Orientation.VERTICAL, max(1, minimum, widget.get_width()))[0],
                "allocated_height": widget.get_height(), "mapped": widget.get_mapped(),
                "orientation": widget.get_orientation().value_nick if isinstance(widget, Gtk.Box) else None}
    return {name: measure(widget) for name, widget in widgets.items()}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, default=Path("/tmp/quantway-ui-qa"))
    parser.add_argument("--width", type=int, default=1000)
    parser.add_argument("--height", type=int, default=740)
    parser.add_argument("--scale", type=float, default=1.0, help="GTK text scaling factor")
    parser.add_argument("--capture", choices=("auto", "gtk", "x11"), default="auto")
    parser.add_argument("--scroll-bottom", action="store_true", help="Also expose the bottom of scrollable content for visual QA")
    parser.add_argument("--states", nargs="+", choices=CORE_STATES + CABINET_STATES + MODAL_STATES + TINC_STATES + ["empty"], default=CORE_STATES + CABINET_STATES + TINC_STATES)
    args = parser.parse_args()
    if not 1 <= args.scale <= 2:
        parser.error("--scale must be between 1 and 2")
    args.output.mkdir(parents=True, exist_ok=True)
    if not Gtk.init_check():
        raise SystemExit("GTK cannot connect to a display. Run this in Ubuntu or an isolated Xvfb session.")
    settings = Gtk.Settings.get_default()
    settings.set_property("gtk-xft-dpi", int(96 * 1024 * args.scale))
    settings.set_property("gtk-enable-animations", False)
    manifest = []
    for state in args.states:
        app = OfflineQuantway()
        if not app.register(None):
            raise RuntimeError("Cannot register isolated QA app")
        app.activate()
        app.window.set_default_size(args.width, args.height)
        app.stack.set_transition_type(Gtk.StackTransitionType.NONE)
        with patch("quantway.threading.Thread"):
            populate(app, state)
        app.window.present()
        settle()
        target = getattr(app, "qa_capture_window", None) or app.window
        if args.scroll_bottom:
            scroll_to_bottom(app.stack.get_visible_child() if target is app.window else target)
            settle()
        path = args.output / f"{state}-{args.width}x{args.height}-scale{args.scale:g}.png"
        backend = capture(target, path, args.capture)
        manifest.append({"state": state, "requested_size": [args.width, args.height],
                         "actual_size": [app.window.get_width(), app.window.get_height()],
                         "scale": args.scale, "capture": backend, "file": path.name,
                         "layout": layout_measurements(app), "capture_size": [target.get_width(), target.get_height()],
                         "scrolled_to_bottom": args.scroll_bottom})
        if app.connection_timer is not None:
            GLib.source_remove(app.connection_timer)
            app.connection_timer = None
        if hasattr(app, "stop_support_poll"):
            app.stop_support_poll()
        app.vpn_process = None
        if target is not app.window:
            target.destroy()
        app.window.destroy()
        app.quit()
        settle(30)
    (args.output / "manifest.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps({"output": str(args.output), "screens": len(manifest)}, ensure_ascii=False))


if __name__ == "__main__":
    main()
