"""Read-only account usage summary; external subscriptions are never included."""
import subprocess
import threading

from gi.repository import GLib, Gtk, Pango

from desktop_ui import box, heading, label
from device_ui import format_usage


class AccountStatsMixin:
    def init_account_stats(self):
        self._account_stats_serial = 0
        self._account_stats_context = None
        self._account_stats_data = None
        self._account_stats_loading = False
        self._account_stats_error = False

    def build_account_stats(self):
        panel = box(8, style="account-stats")
        panel.add_css_class("account-metrics")
        panel.add_css_class("stacked")
        panel.set_hexpand(True)
        panel.append(heading("Устройства и трафик", level=2, style="section-title"))
        grid = Gtk.FlowBox()
        grid.set_selection_mode(Gtk.SelectionMode.NONE)
        grid.set_homogeneous(True)
        grid.set_min_children_per_line(1)
        grid.set_max_children_per_line(2)
        grid.set_column_spacing(12)
        grid.set_row_spacing(6)
        grid.set_hexpand(True)
        self.account_stat_values = {}
        for key, caption in (("places", "Места устройств"), ("online", "Сейчас в сети"),
                             ("month", "Трафик за месяц"), ("self_month", "Этот компьютер")):
            cell = box(2, style="metric-cell")
            cell.set_hexpand(True)
            cell.append(label(caption, "caption", True))
            value = label("—", "stat-value", True)
            value.set_tooltip_text("Статистика ещё не загружена.")
            cell.append(value)
            grid.append(cell)
            cell.get_parent().set_focusable(False)
            self.account_stat_values[key] = value
        panel.append(grid)
        self.account_stats_hint = label("Статистика устройств аккаунта", "caption", True)
        self.account_stats_hint.set_lines(1)
        self.account_stats_hint.set_ellipsize(Pango.EllipsizeMode.END)
        panel.append(self.account_stats_hint)
        return panel

    def reset_account_stats(self):
        self._account_stats_serial += 1
        self._account_stats_context = None
        self._account_stats_data = None
        self._account_stats_loading = False
        self._account_stats_error = False
        self.render_account_stats()

    def refresh_account_stats(self):
        context = self.account_context()
        if not context or self.session.get("identity", {}).get("kind") == "owner":
            return
        if context != self._account_stats_context:
            self._account_stats_data = None
        self._account_stats_context = context
        self._account_stats_serial += 1
        serial = self._account_stats_serial
        self._account_stats_loading = True
        self._account_stats_error = False
        self.render_account_stats()
        def worker():
            try:
                data = self.call_helper("cabinet", {"cookie": context[1], "method": "GET", "path": "devices"})["data"]
                if not isinstance(data, dict):
                    raise ValueError()
                error = False
            except (OSError, subprocess.TimeoutExpired, ValueError, KeyError):
                data, error = {}, True
            GLib.idle_add(self.account_stats_received, context, serial, data, error)
        threading.Thread(target=worker, daemon=True).start()

    def account_stats_page_changed(self, *_args):
        if self.stack.get_visible_child_name() == "account" and self.session and not self._account_stats_loading:
            self.refresh_account_stats()

    def account_stats_received(self, context, serial, data, error=False):
        if not self.account_callback_current(context) or serial != self._account_stats_serial:
            return False
        self._account_stats_loading = False
        self._account_stats_error = error
        if not error:
            self._account_stats_data = data
        self.render_account_stats()
        return False

    def update_account_stats(self, data):
        """Reuse the authoritative device-list response after account actions."""
        context = self.account_context()
        if not context:
            return
        self._account_stats_serial += 1
        self._account_stats_context = context
        self._account_stats_data = data
        self._account_stats_loading = False
        self._account_stats_error = False
        self.render_account_stats()

    @staticmethod
    def account_stat_number(value):
        return value if isinstance(value, int) and not isinstance(value, bool) and value >= 0 else None

    def render_account_stats(self):
        if not hasattr(self, "account_stat_values"):
            return
        data = self._account_stats_data or {}
        current_id = (self.session or {}).get("current_device")
        current = next((item for item in data.get("devices") or [] if current_id and item.get("id") == current_id), {})
        month = self.account_stat_number(data.get("month_bytes"))
        own = self.account_stat_number(current.get("month_bytes"))
        used, paid = (self.account_stat_number(data.get(key)) for key in ("in_use", "paid"))
        online = self.account_stat_number(data.get("online_count"))
        values = {"month": format_usage(month) if month is not None else "—",
                  "self_month": format_usage(own) if own is not None else "—",
                  "places": f"{used} из {paid}" if used is not None and paid is not None else "—",
                  "online": str(online) if online is not None else "—"}
        for key, text in values.items():
            self.account_stat_values[key].set_text(text)
            self.account_stat_values[key].set_tooltip_text(None if text != "—" else "Нет данных от сервиса.")
        self.account_stat_values["month"].set_tooltip_text("Суммарный трафик всех устройств Quantway за текущий месяц.")
        self.account_stat_values["self_month"].set_tooltip_text("Трафик этого компьютера за текущий месяц." if current else "Этого компьютера нет в списке устройств аккаунта." if self._account_stats_data is not None else "Статистика ещё не загружена.")
        self.account_stats_hint.set_text("Обновляем статистику…" if self._account_stats_loading else
                                         "Последние данные · обновление недоступно" if self._account_stats_error and self._account_stats_data is not None else
                                         "Статистика временно недоступна" if self._account_stats_error else
                                         "Только Quantway · за текущий месяц")
        self.account_stats_hint.set_tooltip_text(self.account_stats_hint.get_text())
