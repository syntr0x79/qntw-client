#!/usr/bin/env python3
"""Quiet notices keep page position and full errors keyboard-accessible.

Run in an isolated Linux desktop/Xvfb session:
  xvfb-run -a -s '-screen 0 2000x1600x24' env GSK_RENDERER=cairo \
    python3 test_notice_layout.py --output /tmp/quantway-notice-qa

The test changes only in-memory status text. Subscription loading, probe/VPN
workers and account mutations are blocked by OfflineQuantway and mock Thread.
PNG snapshots and a JSON report include real widget bounds after allocation.
"""
from __future__ import annotations

import argparse
import json
from pathlib import Path
from unittest.mock import patch

from qa_screens import OfflineQuantway, capture, populate, settle
from gi.repository import GLib, Gtk


PROFILES = {
    "normal": (1000, 740, 1),
    "narrow-large-text": (740, 620, 2),
}
LONG_ERROR = (
    "Не удалось связаться с сервисом. Проверьте подключение к сети и повторите попытку. "
    "Существующие маршруты сохранены; для подключения требуется новая проверка.\n"
    "Вторая строка диагностики.\nТретья строка диагностики.\n"
    "Четвёртая строка диагностики.\nПятая строка диагностики.\n"
    "Дополнительные сведения для диагностики: "
    "https://example.invalid/notice-layout/" + "very-long-diagnostic-segment-" * 12
)
VARIANTS = (
    ("empty", ""),
    ("short", "Маршрут выбран."),
    ("long-error", LONG_ERROR),
    ("cleared", ""),
)


def descendants(widget):
    yield widget
    child = widget.get_first_child()
    while child:
        yield from descendants(child)
        child = child.get_next_sibling()


def bounds(widget, window):
    success, rectangle = widget.compute_bounds(window)
    if not success:
        raise AssertionError(f"Cannot compute mapped bounds of {type(widget).__name__}")
    return [round(rectangle.get_x(), 3), round(rectangle.get_y(), 3),
            round(rectangle.get_width(), 3), round(rectangle.get_height(), 3)]


def page_title(app):
    current = app.stack.get_visible_child()
    for widget in descendants(current):
        if isinstance(widget, Gtk.Label) and widget.has_css_class("page-title"):
            return widget
    raise AssertionError("The visible page must have a page title")


def geometry(app):
    return {
        "window": [app.window.get_width(), app.window.get_height()],
        "notice": bounds(app.notice, app.window) if app.notice.get_mapped() else None,
        "stack": bounds(app.stack, app.window),
        "page_title": bounds(page_title(app), app.window),
    }


def rendered_notice(app):
    root = app.notice_stack.get_visible_child() if hasattr(app, "notice_stack") else app.notice
    labels = [widget for widget in descendants(root)
              if isinstance(widget, Gtk.Label) and widget.get_mapped()]
    return "\n".join(widget.get_text() for widget in labels).strip()


def notice_tooltips(app):
    return [widget.get_tooltip_text() for widget in descendants(app.notice)
            if widget.get_mapped() and widget.get_tooltip_text()]


def cleanup(app):
    if app.connection_timer is not None:
        GLib.source_remove(app.connection_timer)
        app.connection_timer = None
    app.vpn_process = None
    app.window.destroy()
    app.quit()
    settle(30)


def check_profile(profile_name, states, output, failures):
    width, height, scale = PROFILES[profile_name]
    settings = Gtk.Settings.get_default()
    settings.set_property("gtk-xft-dpi", 96 * 1024 * scale)
    settings.set_property("gtk-enable-animations", False)
    rows = []
    for state in states:
        app = OfflineQuantway()
        if not app.register(None):
            raise AssertionError("Cannot register isolated layout test app")
        app.activate()
        app.window.set_default_size(width, height)
        app.stack.set_transition_type(Gtk.StackTransitionType.NONE)
        populate(app, state)
        app.window.present()
        settle()
        baseline = None
        try:
            for variant, message in VARIANTS:
                app.status.set_text(message)
                settle()
                current = geometry(app)
                visible_text = rendered_notice(app)
                issues = []
                expected_visible = bool(message)
                if app.notice.get_visible() != expected_visible:
                    issues.append("notice visibility does not follow explicit messages")
                if expected_visible and not visible_text:
                    issues.append("explicit message has no visible text")
                if not expected_visible and visible_text:
                    issues.append("routine hint still occupies the notice area")
                if current["window"][0] > width:
                    issues.append(f"window expanded beyond requested width: {current['window'][0]} > {width}")
                if baseline is None:
                    baseline = current
                elif current["page_title"][:2] != baseline["page_title"][:2]:
                    issues.append("showing a bottom notice moved the page heading")
                if current["stack"][:3] != baseline["stack"][:3]:
                    issues.append("notice changed the page's horizontal bounds or top edge")
                if current["stack"][3] < 150:
                    issues.append("notice leaves too little room for page content")
                if expected_visible:
                    x, y, w, h = current["notice"]
                    if x < 0 or y < 0 or x + w > current["window"][0] or y + h > current["window"][1]:
                        issues.append("notice is clipped outside the window")
                if variant == "long-error":
                    if not app.notice_details.get_mapped() or not app.notice_details.get_sensitive():
                        issues.append("full-message action is unavailable")
                    app.notice_details.emit("clicked")
                    dialog = app.notice_dialog
                    full_labels = [widget for widget in descendants(dialog) if isinstance(widget, Gtk.Label)
                                   and widget.get_selectable() and widget.get_text() == LONG_ERROR]
                    if not full_labels:
                        issues.append("full error is not readable/selectable in its dialog")
                    dialog.response(Gtk.ResponseType.CLOSE)
                    settle(30)
                if variant == "cleared" and app.notice.has_css_class("error"):
                    issues.append("error style remains after clearing status")
                filename = f"{profile_name}-{state}-{variant}.png"
                backend = capture(app.window, output / filename, "auto")
                row = {"profile": profile_name, "state": state, "variant": variant,
                       "requested_size": [width, height], "text_scale": scale,
                       "geometry": current, "visible_text": visible_text,
                       "file": filename, "capture": backend, "issues": issues}
                rows.append(row)
                failures.extend(f"{profile_name}/{state}/{variant}: {issue}" for issue in issues)
        finally:
            cleanup(app)
    return rows


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", type=Path, default=Path("/tmp/quantway-notice-qa"))
    parser.add_argument("--profiles", nargs="+", choices=tuple(PROFILES), default=list(PROFILES))
    parser.add_argument("--states", nargs="+", choices=("ready", "checking", "routes", "account", "plans"),
                        default=["ready", "checking", "account"])
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    if not Gtk.init_check():
        raise SystemExit("GTK cannot connect to a display. Run under Xvfb or in an isolated Linux desktop.")
    failures = []
    screenshots = []
    # No background production operation can start while populating or changing
    # a fixture. Rendering and GLib allocations run on the main thread.
    with patch("quantway.threading.Thread"):
        for profile_name in args.profiles:
            screenshots.extend(check_profile(profile_name, args.states, args.output, failures))
    report = {"passed": not failures, "failures": failures, "screenshots": screenshots}
    (args.output / "report.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
    for failure in failures:
        print(f"FAIL: {failure}")
    print(json.dumps({"passed": not failures, "screenshots": len(screenshots),
                      "output": str(args.output)}, ensure_ascii=False))
    raise SystemExit(1 if failures else 0)


if __name__ == "__main__":
    main()
