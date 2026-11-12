"""Quantway-only connection mode; the GTK process never changes the network."""
import json
import subprocess
import threading
import time

from gi.repository import GLib, Gtk

from auth_persistence import ServiceFailure
from platform_runtime import helper_path, helper_process_options
from desktop_ui import box, button, field, label, action_row, heading as title_label, status_label, icon_button, disclosure
from mesh_ui import mesh_snapshot, format_traffic, traffic_rates


# Labels are presentation only. Eligibility and available codes come from API.
COUNTRY_NAMES = {"PL": "Польша", "SE": "Швеция", "DE": "Германия", "NL": "Нидерланды",
                 "FI": "Финляндия", "FR": "Франция", "GB": "Великобритания", "US": "США",
                 "CA": "Канада", "CH": "Швейцария", "EE": "Эстония", "LV": "Латвия",
                 "LT": "Литва", "TR": "Турция", "JP": "Япония", "SG": "Сингапур"}


def country_name(code):
    return COUNTRY_NAMES.get(code, code)


# Fixed local messages only; never display arbitrary error text from a helper.
TINC_FAILURE_MESSAGES = {
    "run_as_user": "Запустите Quantway обычным пользователем, без sudo.",
    "helper_missing": "Сетевой помощник Tinc не установлен. Запустите setup-vm.sh из этого архива.",
    "helper_unsafe": "Неверные права сетевого помощника Tinc. Повторите установку через setup-vm.sh.",
    "native_missing": "Компоненты Tinc не установлены. Повторите установку через setup-vm.sh.",
    "pkexec_missing": "Не установлен PolicyKit (pkexec). Установите его и повторите подключение.",
    "pkexec_unsafe": "Неверные права PolicyKit (pkexec). Проверьте установку системного пакета.",
    "authorization_cancelled": "Запрос разрешения администратора отменён.",
    "authorization_failed": "Не удалось получить разрешение администратора. Проверьте PolicyKit и системный запрос.",
    "helper_start_failed": "Не удалось запустить сетевой помощник Tinc. Повторите установку.",
    "helper_protocol_failed": "Нарушена связь с сетевым помощником Tinc. Переустановите помощники из текущего архива.",
    "helper_initialization_failed": "Сетевой помощник Tinc не смог запуститься. Повторите установку.",
    "device_authorization_failed": "Не удалось проверить доступ этого устройства к Tinc. Повторите попытку.",
    "device_state_unavailable": "Не удалось открыть защищённые данные Tinc. Обратитесь в поддержку.",
    "vpn_busy": "Другое VPN-подключение ещё работает. Сначала отключите его.",
    "vpn_lock_failed": "Не удалось занять сетевой помощник. Закройте другое VPN-подключение и повторите попытку.",
    "network_setup_failed": "Не удалось настроить сеть для Tinc. Проверьте установку и поддержку VPN в системе.",
    "cleanup_failed": "Не удалось полностью восстановить сетевые настройки. Новое подключение не запущено.",
    "identity_conflict": "Ключ Tinc этого устройства не совпадает с сохранённым на сервере. Обратитесь в поддержку.",
    "identity_failed": "Не удалось открыть или создать ключ Tinc. Обратитесь в поддержку.",
    "invalid_device_token": "Ссылка этого компьютера недействительна. Обновите её в кабинете.",
    "subscription_inactive": "Продлите или возобновите подписку, чтобы подключиться.",
    "access_denied": "Доступ к Tinc для этого устройства ограничен. Обратитесь в поддержку.",
    "dns_unavailable": "Не удалось настроить DNS для Tinc. Проверьте работу systemd-resolved.",
    "network_tools_missing": "Не найдены iproute2 или nftables. Установите их и повторите подключение.",
    "tun_unavailable": "Не удалось открыть VPN-интерфейс. Проверьте поддержку TUN в системе.",
    "route_conflict": "Обнаружены сетевые настройки другого VPN. Отключите его и повторите попытку."
}


class TincMixin:
    def init_tinc_state(self):
        self.connection_mode = "ordinary"
        self.tinc_catalog = []
        self.tinc_catalog_state = "idle"
        self.tinc_country = ""
        self.tinc_status = {}
        self.tinc_catalog_context = None
        self.tinc_catalog_serial = 0
        self.tinc_catalog_loading = False
        self.tinc_catalog_error = ""
        self.tinc_worker_active = False
        self.tinc_stopping = False
        self.tinc_change_dialog = None
        self.pending_mode_change = None
        self._tinc_rendering = False
        self._tinc_selection_context = None
        self._tinc_auto_connect = False
        self._tinc_traffic_sample = None
        self._tinc_traffic_rates = (None, None)
        self.tinc_mesh_timer = None

    def tinc_source_context(self):
        source, session = self.active_source or {}, self.session or {}
        link = session.get("device_link") or ""
        if (source.get("kind") != "quantway" or not session.get("current_device") or
                source.get("id") != session["current_device"] or not link or
                source.get("link") != link or self.entry.get_text().strip() != link):
            return None
        return (self.account_generation, session.get("cookie"), session["current_device"], link)

    def tinc_selected(self):
        return self.connection_mode == "tinc" and self.tinc_source_context() is not None

    def build_tinc_home_controls(self):
        # Start stacked so the initial GTK minimum cannot widen the window
        # before responsive measurement has an allocated viewport to use.
        self.tinc_controls = box(16)
        modes = box(0, True, "connection-modes")
        modes.set_halign(Gtk.Align.START)
        self.tinc_ordinary_button = Gtk.ToggleButton(label="Обычный VPN")
        self.tinc_mode_button = Gtk.ToggleButton(label="Tinc")
        self.tinc_mode_button.set_group(self.tinc_ordinary_button)
        self.tinc_ordinary_button.set_active(True)
        for widget, mode in ((self.tinc_ordinary_button, "ordinary"), (self.tinc_mode_button, "tinc")):
            widget.add_css_class("mode-option")
            widget.connect("toggled", self.connection_mode_requested, mode)
            modes.append(widget)
        self.tinc_mode_field = field("Способ подключения", modes)
        self.tinc_mode_field.set_valign(Gtk.Align.START)
        self.tinc_controls.append(self.tinc_mode_field)
        self.tinc_country_picker = Gtk.DropDown.new_from_strings(["Выберите страну"])
        self.tinc_country_picker.set_hexpand(True)
        self.tinc_country_picker.connect("notify::selected", self.tinc_country_changed)
        country_row = box(8, True)
        country_row.set_size_request(260, -1)
        country_row.append(self.tinc_country_picker)
        self.tinc_catalog_retry = icon_button("view-refresh-symbolic", "Обновить список стран", lambda _b: self.request_tinc_catalog())
        country_row.append(self.tinc_catalog_retry)
        self.tinc_country_field = field("_Страна выхода", country_row)
        self.tinc_country_field.set_hexpand(False)
        self.tinc_country_field.get_first_child().set_mnemonic_widget(self.tinc_country_picker)
        self.tinc_country_picker.update_property([Gtk.AccessibleProperty.LABEL], ["Страна выхода Tinc"])
        self.tinc_controls.append(self.tinc_country_field)
        self.tinc_connect_button = self.connect_button
        self.tinc_mesh_button = button("Открыть монитор Tinc", self.open_tinc_page, "secondary-link")
        self.tinc_controls.set_visible(False)
        self.tinc_mesh_button.set_visible(False)
        return self.tinc_controls

    def render_tinc_modes(self):
        previous, self._tinc_rendering = self._tinc_rendering, True
        try:
            selected = self.tinc_mode_button if self.connection_mode == "tinc" else self.tinc_ordinary_button
            selected.set_active(True)
        finally:
            self._tinc_rendering = previous

    def connection_mode_requested(self, widget, target):
        if self._tinc_rendering or not widget.get_active():
            return
        self.render_tinc_modes()
        if target == self.connection_mode or not self.tinc_source_context():
            return
        if (self.vpn_starting or self.loading_profiles or self.probe_running or self.switching_route or
                self.tinc_stopping or self.tinc_change_dialog or getattr(self, "_device_mutating", False)):
            return
        if self.vpn_process is not None:
            self.confirm_tinc_change("mode", target)
        elif not self.tinc_worker_active and self._vpn_worker_generation is None:
            self.apply_tinc_mode(target)

    def build_tinc_page(self):
        page = box(18)
        heading = box(12, True)
        title = title_label("Tinc")
        title.set_hexpand(True)
        heading.append(title)
        self.tinc_page_state = status_label("Не подключён", "state-pill")
        self.tinc_page_state.set_valign(Gtk.Align.CENTER)
        heading.append(self.tinc_page_state)
        page.append(heading)
        self.tinc_eligibility = box(12, style="quiet-panel")
        self.tinc_eligibility.append(label("Доступно для этого компьютера в Quantway", "section-title", True))
        self.tinc_eligibility.append(label("Войдите в кабинет и выберите подписку этого компьютера. Подписки других провайдеров работают в обычном режиме.", "muted", True))
        self.tinc_eligibility.append(button("Открыть кабинет", lambda _b: self.nav_buttons[2].set_active(True), "primary"))
        page.append(self.tinc_eligibility)
        self.tinc_mesh_panel = box(10, style="quiet-panel")
        self.tinc_mesh_panel.add_css_class("mesh-summary")
        self.tinc_mesh_path = label("", "mesh-path", True)
        self.tinc_mesh_panel.append(self.tinc_mesh_path)
        self.tinc_mesh_egress = label("", "metric-value", True)
        self.tinc_mesh_panel.append(self.tinc_mesh_egress)
        self.tinc_mesh_path_hint = label("", "muted", True)
        traffic = box(20, True)
        self.tinc_received = label("Получено: —", "metric-value", True)
        self.tinc_sent = label("Отправлено: —", "metric-value", True)
        traffic.append(self.tinc_received)
        traffic.append(self.tinc_sent)
        self.tinc_mesh_panel.append(traffic)
        self.tinc_traffic_hint = label("", "caption", True)
        page.append(self.tinc_mesh_panel)
        self.tinc_mesh_header = title_label("Входы", 2)
        page.append(self.tinc_mesh_header)
        self.tinc_mesh_observation = label("", "caption", True)
        page.append(self.tinc_mesh_observation)
        self.tinc_mesh_nodes = box(0, style="mesh-nodes")
        self._tinc_node_widgets = {}
        page.append(self.tinc_mesh_nodes)
        self.tinc_mesh_empty = label("Подключите Tinc на «Главной», чтобы увидеть соединения.", "muted", True)
        page.append(self.tinc_mesh_empty)
        explanations = box(8)
        explanations.append(self.tinc_mesh_path_hint)
        explanations.append(self.tinc_traffic_hint)
        page.append(disclosure("Как читать данные меша", explanations))
        page.append(action_row(button("Управлять подключением на Главной", self.open_connection_home, "secondary-link")))
        return page

    def open_connection_home(self, _button=None):
        self.nav_by_page["home"].set_active(True)

    def render_tinc_mesh(self):
        view = mesh_snapshot(self.tinc_status, self.tinc_worker_active)
        self.tinc_mesh_panel.set_visible(self.tinc_source_context() is not None or self.tinc_worker_active)
        self.tinc_mesh_empty.set_visible(not view["nodes"])
        country = country_name(view["country"]) if view["country"] else "Назначенный выход"
        self.tinc_mesh_egress.set_visible(bool(view["egress_name"]))
        self.tinc_mesh_egress.set_text("Выход: " + country + " · " + view["egress_name"])
        self.tinc_mesh_egress.set_tooltip_text(view["egress_id"] or None)
        if view["forwarding"]:
            self.tinc_mesh_path.set_text("Этот компьютер  →  Меш входов  →  " + country + "  →  Интернет")
            self.tinc_mesh_path_hint.set_text("Входы ниже подтверждают соединения с мешем. Внутренние переходы между серверами пока не отображаются.")
        elif view["active"]:
            self.tinc_mesh_path.set_text("Проверяем доступность выхода" if self.tinc_status.get("status") == "preparing" else "Восстанавливаем путь через меш")
            self.tinc_mesh_path_hint.set_text("Назначенный выход: " + country + ". Доступность входов показана ниже.")
        else:
            self.tinc_mesh_path.set_text("Tinc не подключён")
            self.tinc_mesh_path_hint.set_text("Выберите Tinc и страну выхода на «Главной».")
        sample = view["traffic_sample"]
        if sample != self._tinc_traffic_sample:
            self._tinc_traffic_rates = traffic_rates(self._tinc_traffic_sample, sample)
            self._tinc_traffic_sample = sample
        rx_rate, tx_rate = self._tinc_traffic_rates
        self.tinc_received.set_text("Получено: " + format_traffic(view["rx_bytes"]) +
                                    ("\n↓ " + format_traffic(rx_rate) + "/с" if rx_rate is not None else ""))
        self.tinc_sent.set_text("Отправлено: " + format_traffic(view["tx_bytes"]) +
                               ("\n↑ " + format_traffic(tx_rate) + "/с" if tx_rate is not None else ""))
        self.tinc_traffic_hint.set_text("Локальные счётчики за текущее подключение." if view["rx_bytes"] is not None else
                                      "Счётчики появятся после получения данных от сетевого помощника.")
        total = len(view["nodes"])
        self.tinc_mesh_header.set_text(f"Входы: {total} · нет свежих данных" if view["complete"] and not view["fresh"] else
                                       f"Входы: {view['connected']} из {total}" if view["complete"] else
                                       f"Подключённые входы: {view['connected']}" if view["nodes"] else "Входы")
        self.tinc_mesh_observation.set_text(view["observation"])
        keys = tuple(item["name"] for item in view["nodes"])
        if keys != tuple(self._tinc_node_widgets):
            while self.tinc_mesh_nodes.get_first_child():
                self.tinc_mesh_nodes.remove(self.tinc_mesh_nodes.get_first_child())
            self._tinc_node_widgets = {}
            for item in view["nodes"]:
                row = box(12, True, "mesh-node")
                name = label(item["name"], "row-title", True)
                name.set_hexpand(True)
                row.append(name)
                state = label("", "status-pill")
                row.append(state)
                self.tinc_mesh_nodes.append(row)
                self._tinc_node_widgets[item["name"]] = state
        for item in view["nodes"]:
            state = self._tinc_node_widgets[item["name"]]
            state.set_text("Подключён" if item["connected"] is True else "Нет соединения" if item["connected"] is False else "Нет свежих данных")
            for style in ("success", "warning"):
                state.remove_css_class(style)
            if item["connected"] is not None:
                state.add_css_class("success" if item["connected"] else "warning")
        return view

    def open_tinc_page(self, _button=None):
        if not self.nav_by_page["tinc"].get_active():
            self.nav_by_page["tinc"].set_active(True)
            return
        self.stack.set_visible_child_name("tinc")
        if self.tinc_source_context() and self.tinc_catalog_context != self.tinc_source_context():
            self.request_tinc_catalog()
        self.status.set_text("")
        self.sync_controls()

    def toggle_tinc_page(self, _button=None):
        if self.tinc_worker_active:
            self.toggle_tinc_vpn()
        elif self.vpn_process is not None:
            self.confirm_tinc_change("mode", "tinc")
        elif not (self.vpn_starting or self.loading_profiles or self.probe_running):
            self.connection_mode = "tinc"
            self.toggle_tinc_vpn()

    def tinc_ordinary_requested(self, _button=None):
        if self.tinc_worker_active and self.vpn_process is not None:
            self.confirm_tinc_change("mode", "ordinary")
        elif not self.vpn_starting:
            self.apply_tinc_mode("ordinary")
            self.nav_buttons[0].set_active(True)
            self.stack.set_visible_child_name("home")

    def render_tinc_choices(self):
        self._tinc_rendering = True
        try:
            names = []
            for item in self.tinc_catalog:
                suffix = {"preparing": " · подготовка", "unavailable": " · недоступна"}.get(item["state"], "")
                names.append(country_name(item["code"]) + suffix)
            names = names or ["Выберите страну"]
            model = self.tinc_country_picker.get_model()
            if model is None or [model.get_string(i) for i in range(model.get_n_items())] != names:
                self.tinc_country_picker.set_model(Gtk.StringList.new(names))
            index = next((i for i, item in enumerate(self.tinc_catalog) if item["code"] == self.tinc_country), 0)
            if self.tinc_country_picker.get_selected() != index:
                self.tinc_country_picker.set_selected(index)
        finally:
            self._tinc_rendering = False

    def request_tinc_catalog(self):
        context = self.tinc_source_context()
        if not context or self.tinc_catalog_loading:
            return
        self.tinc_catalog_serial += 1
        serial = self.tinc_catalog_serial
        self.tinc_catalog_context = context
        self.tinc_catalog_loading = True
        self.tinc_catalog_error = ""
        self.sync_controls()
        threading.Thread(target=self.tinc_catalog_worker, args=(context, serial), daemon=True).start()

    def tinc_catalog_worker(self, context, serial):
        error = ""
        data = {}
        try:
            data = self.call_helper("tinc-catalog", {"link": context[-1]})["data"]
        except ServiceFailure as exc:
            error = {403: "Доступ к Tinc для этого устройства ограничен. Обратитесь в поддержку.",
                     401: "Ссылка этого устройства больше не действует. Обновите данные кабинета.",
                     402: "Продлите подписку, чтобы подключиться в режиме tinc."}.get(exc.status,
                         "Не удалось получить страны. Попробуйте обновить список.")
            if exc.status == 503 and exc.code == "capacity_exhausted":
                error = "Все подключения заняты. Повторите позже."
        except (OSError, ValueError, KeyError, subprocess.TimeoutExpired):
            error = "Не удалось получить страны. Попробуйте обновить список."
        GLib.idle_add(self.tinc_catalog_received, data, error, context, serial)

    def tinc_catalog_received(self, data, error, context, serial):
        if self.closing or serial != self.tinc_catalog_serial or context != self.tinc_source_context():
            return False
        self.tinc_catalog_loading = False
        choices = data.get("countries", [])
        valid = (data.get("schema_version") == 1 and data.get("state") in
                 ("available", "preparing", "unavailable") and isinstance(choices, list))
        parsed = []
        if valid:
            for item in choices:
                if (not isinstance(item, dict) or not isinstance(item.get("code"), str) or
                        len(item["code"]) != 2 or not item["code"].isascii() or not item["code"].isupper() or
                        not item["code"].isalpha() or item.get("state") not in ("available", "preparing", "unavailable") or
                        item["code"] in {country["code"] for country in parsed}):
                    valid = False
                    break
                parsed.append({"code": item["code"], "state": item["state"]})
        self.tinc_catalog_error = error or ("Не удалось получить страны. Попробуйте обновить список." if not valid else "")
        self.tinc_catalog = parsed if valid and not error else []
        self.tinc_catalog_state = data.get("state", "unavailable") if valid and not error else "unavailable"
        codes = {item["code"] for item in self.tinc_catalog}
        if self.tinc_country not in codes:
            requested = data.get("requested_country")
            self.tinc_country = requested if requested in codes else next(
                (item["code"] for item in self.tinc_catalog if item["state"] != "unavailable"), "")
        self.render_tinc_choices()
        if self.tinc_selected():
            self.status.set_text(self.tinc_catalog_error, kind="error" if self.tinc_catalog_error else None)
        self.sync_controls()
        if self._tinc_auto_connect:
            self._tinc_auto_connect = False
            if self.tinc_can_connect():
                self.toggle_tinc_vpn()
        return False

    def tinc_can_connect(self):
        return (self.tinc_source_context() is not None and self.tinc_catalog_state != "unavailable" and not self.tinc_catalog_loading and not self.tinc_catalog_error and
                any(item["code"] == self.tinc_country and item["state"] != "unavailable" for item in self.tinc_catalog))

    def apply_tinc_mode(self, target):
        self.connection_mode = target
        self.tinc_status = {}
        self.status.set_text("")
        self.render_tinc_choices()
        if target == "tinc" and self.tinc_catalog_context != self.tinc_source_context():
            self.request_tinc_catalog()
        self.sync_controls()
        if target == "ordinary":
            self.update_route_card()
            self.connection_title.set_text("Готово к подключению" if not self.results_stale and self.selected_profile else "Проверьте маршруты")
            self.connection_description.set_text("Выберите проверенный маршрут для обычного подключения.")

    def tinc_country_changed(self, picker, _property):
        if self._tinc_rendering or not self.tinc_source_context() or picker.get_selected() >= len(self.tinc_catalog):
            return
        target = self.tinc_catalog[picker.get_selected()]["code"]
        self.render_tinc_choices()
        if target == self.tinc_country:
            return
        if self.tinc_stopping or self.tinc_status.get("status") in ("preparing", "waiting", "reconnecting", "failed"):
            return
        if self.tinc_worker_active and self.vpn_process is not None:
            self.confirm_tinc_change("country", target)
        elif not self.vpn_starting:
            self.tinc_country = target
            self.render_tinc_choices()
            self.sync_controls()

    def confirm_tinc_change(self, kind, target):
        if not self.tinc_source_context() or self.tinc_change_dialog or self.route_switch_dialog or self.switching_route:
            return
        if kind == "country":
            text, detail = "Сменить страну?", "Подключиться через «" + country_name(target) + "»? Текущее соединение будет ненадолго прервано."
        else:
            text, detail = "Сменить режим подключения?", "Текущее VPN-соединение будет отключено. Затем приложение подключится в выбранном режиме."
        dialog = Gtk.MessageDialog(transient_for=self.window, modal=True, buttons=Gtk.ButtonsType.NONE,
                                   message_type=Gtk.MessageType.QUESTION, text=text)
        dialog.set_property("secondary-text", detail)
        dialog.add_button("Отмена", Gtk.ResponseType.CANCEL)
        dialog.add_button("Переключить", Gtk.ResponseType.ACCEPT)
        self.tinc_change_dialog = dialog
        dialog.connect("response", self.tinc_change_response, kind, target, self.tinc_source_context())
        self.sync_controls()
        dialog.present()

    def tinc_change_response(self, dialog, response, kind, target, context):
        dialog.destroy()
        self.tinc_change_dialog = None
        if response != Gtk.ResponseType.ACCEPT or self.closing or not context or context != self.tinc_source_context():
            self.sync_controls()
            return
        try:
            if kind == "country":
                if not self.tinc_worker_active or self.vpn_process is None or target not in {item["code"] for item in self.tinc_catalog}:
                    return
                self.vpn_process.stdin.write(json.dumps({"country": target}) + "\n")
                self.vpn_process.stdin.flush()
                self.tinc_country = target
                self.tinc_status = dict(self.tinc_status, status="reconnecting", desired_country=target)
            else:
                if self.vpn_process is None:
                    return
                self.pending_mode_change = (target, context)
                self.switching_route = True
                self.vpn_process.stdin.write("stop\n")
                self.vpn_process.stdin.flush()
        except (OSError, ValueError, AttributeError):
            self.pending_mode_change = None
            self.switching_route = False
            self.status.set_text("Не удалось переключить подключение. Отключите VPN и попробуйте снова.")
        self.render_tinc_choices()
        self.sync_controls()

    def toggle_tinc_vpn(self):
        if self.vpn_process is not None:
            try:
                self.vpn_process.stdin.write("stop\n")
                self.vpn_process.stdin.flush()
                self.tinc_stopping = True
            except (OSError, ValueError, AttributeError):
                self.status.set_text("Не удалось отключить VPN. Повторите отключение.")
            self.sync_controls()
            return
        if (not self.tinc_can_connect() or self.vpn_starting or self.loading_profiles or self.probe_running or
                self.switching_route or getattr(self, "_device_mutating", False)):
            return
        context = self.tinc_source_context()
        self.tinc_worker_active = True
        self.tinc_stopping = False
        self.tinc_status = {"status": "preparing", "desired_country": self.tinc_country}
        self.status.set_text("")
        self.vpn_starting = True
        self.vpn_generation += 1
        self._vpn_worker_generation = self.vpn_generation
        self.sync_controls()
        threading.Thread(target=self.tinc_vpn_worker, args=(context, self.tinc_country, self.vpn_generation), daemon=True).start()

    def tinc_vpn_worker(self, context, country, generation):
        helper = helper_path()
        process, error, connected = None, "", False
        try:
            if self.closing or generation != self.vpn_generation:
                return
            process = subprocess.Popen([helper, "--json", "--action", "tinc-connect"], stdin=subprocess.PIPE,
                                       stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True, **helper_process_options())
            self.vpn_process = process
            if self.closing or generation != self.vpn_generation:
                return
            process.stdin.write(json.dumps({"link": context[-1], "country": country}) + "\n")
            process.stdin.flush()
            for line in process.stdout:
                event = json.loads(line)
                if not isinstance(event, dict) or event.get("status") not in ("preparing", "waiting", "connected", "reconnecting", "expired", "failed"):
                    raise ValueError("Invalid helper status")
                connected = connected or event["status"] == "connected"
                if event["status"] == "failed":
                    error = TINC_FAILURE_MESSAGES.get(event.get("code"),
                        "Не удалось полностью восстановить сетевые настройки. Повторите отключение перед сменой режима."
                        if "восстановить сетевые настройки" in str(event.get("error", "")) else
                        "Не удалось подключиться в режиме tinc. Попробуйте ещё раз.")
                GLib.idle_add(self.tinc_vpn_event, event, generation, context)
            process.wait()
            if process.returncode and not error:
                error = "Подключение tinc завершилось. Попробуйте подключиться снова."
        except (OSError, ValueError, KeyError, BrokenPipeError):
            error = "Не удалось запустить подключение tinc. Попробуйте ещё раз."
        finally:
            if process is not None:
                self.reap_helper(process)
            if self.vpn_process is process:
                self.vpn_process = None
            GLib.idle_add(self.vpn_worker_finished, error, connected, generation)

    def tinc_vpn_event(self, event, generation, context):
        if self.closing or generation != self.vpn_generation or context != self.tinc_source_context():
            return False
        self.tinc_status = event
        if event.get("mesh") is not None and self.tinc_mesh_timer is None:
            self.tinc_mesh_timer = GLib.timeout_add_seconds(2, self.refresh_tinc_mesh)
        self.status.set_text("")
        self.vpn_starting = False
        if event["status"] == "connected":
            if self.connected_since is None:
                self.connected_since = time.monotonic()
            if self.connection_timer is None:
                self.connection_timer = GLib.timeout_add_seconds(1, self.update_connection_time)
            country = event.get("desired_country") or event.get("country")
            if country in {item["code"] for item in self.tinc_catalog}:
                self.tinc_country = country
                self.render_tinc_choices()
        else:
            self.connected_since = None
        self.sync_controls()
        return False

    def refresh_tinc_mesh(self):
        if self.closing or not self.tinc_worker_active or not self.window.get_visible():
            self.tinc_mesh_timer = None
            return False
        self.sync_tinc_controls()
        return True

    def finish_tinc_transition(self, error, connected):
        was_tinc = self.tinc_worker_active
        self.tinc_worker_active = False
        self.tinc_stopping = False
        if self.tinc_mesh_timer is not None:
            GLib.source_remove(self.tinc_mesh_timer)
            self.tinc_mesh_timer = None
        pending, self.pending_mode_change = self.pending_mode_change, None
        if pending and error:
            self.switching_route = False
            self.tinc_status = {"status": "failed"} if was_tinc else self.tinc_status
            self.status.set_text(error, kind="error")
            self.sync_controls()
            return True
        if pending and pending[1] == self.tinc_source_context():
            self.switching_route = False
            self.apply_tinc_mode(pending[0])
            if pending[0] == "tinc":
                if self.tinc_catalog_loading:
                    self._tinc_auto_connect = True
                elif self.tinc_can_connect():
                    self.toggle_tinc_vpn()
            else:
                self.nav_buttons[0].set_active(True)
                self.stack.set_visible_child_name("home")
                if not self.results_stale and self.selected_profile:
                    self.toggle_vpn(None)
                else:
                    self.status.set_text("Обычный режим выбран. Проверьте маршруты перед подключением.")
            return True
        if was_tinc:
            self.tinc_status = {"status": "failed"} if error else {}
            self.status.set_text(error or "VPN отключён.", kind="error" if error else "info")
            self.sync_controls()
            self.load_pending_account_subscription()
            return True
        return False

    def tinc_hint(self):
        if self.tinc_stopping:
            return "Отключаем VPN и восстанавливаем сеть."
        state = self.tinc_status.get("status")
        if state == "expired":
            return "Доступ не удалось продлить. Интернет заблокирован до повторного подключения или нажатия «Отключить»."
        if self.tinc_status.get("code") == "capacity_exhausted":
            return ("Все подключения заняты. Текущее соединение сохранено." if state == "connected" else
                    "Все подключения заняты. Повторите позже.")
        if state == "reconnecting":
            return "Восстанавливаем соединение. Интернет защищён от обхода VPN."
        if state == "connected":
            return "Соединение активно. Страну можно изменить с подтверждением."
        if state == "failed":
            return TINC_FAILURE_MESSAGES.get(self.tinc_status.get("code"),
                "Не удалось подключиться в режиме tinc. Попробуйте ещё раз или выберите обычный режим.")
        if state in ("preparing", "waiting"):
            return "Подготавливаем защищённое подключение. Это может занять немного времени."
        if self.tinc_catalog_error:
            return self.tinc_catalog_error
        if self.tinc_catalog_loading:
            return "Получаем доступные страны…"
        if not self.tinc_catalog:
            return "В режиме tinc пока нет доступных стран. Обновите список позже."
        return "Выберите страну. Приложение подключится ко всем доступным входам и назначенному выходу."

    def sync_tinc_controls(self):
        context = self.tinc_source_context()
        if context != self._tinc_selection_context and not self.tinc_worker_active and not self.vpn_process:
            self._tinc_selection_context = context
            self.tinc_catalog_serial += 1
            self.tinc_catalog_loading = False
            self.tinc_catalog_context = None
            self.tinc_catalog = []
            self.tinc_country = ""
            self.tinc_catalog_error = ""
            self._tinc_auto_connect = False
            if context is not None or (self.active_source or {}).get("kind") == "external":
                self.connection_mode = "ordinary"
            self.render_tinc_choices()
        self.render_tinc_modes()
        show_tinc = self.tinc_worker_active or (self.connection_mode == "tinc" and self.vpn_process is None)
        self.tinc_controls.set_visible(context is not None or self.tinc_worker_active)
        self.tinc_eligibility.set_visible(context is None and not self.tinc_worker_active)
        self.tinc_country_field.set_visible(show_tinc)
        self.tinc_mesh_button.set_visible(show_tinc)
        self.tinc_catalog_retry.set_visible(show_tinc and not self.tinc_worker_active)
        self.home_columns.set_visible(not show_tinc)
        self.ordinary_route_summary.set_visible(not show_tinc)
        self.probe_button.set_visible(not show_tinc)
        locked = (context is None or self.vpn_starting or self.probe_running or self.loading_profiles or self.switching_route or
                  bool(self.tinc_change_dialog) or self.tinc_stopping or getattr(self, "_device_mutating", False))
        state = self.tinc_status.get("status")
        if self.tinc_worker_active and state in ("preparing", "waiting", "reconnecting", "failed"):
            locked = True
        self.tinc_ordinary_button.set_sensitive(not locked)
        self.tinc_mode_button.set_sensitive(not locked)
        self.tinc_country_picker.set_sensitive(not locked and not self.tinc_catalog_loading and bool(self.tinc_catalog))
        self.tinc_catalog_retry.set_sensitive(not locked and not self.tinc_catalog_loading)
        self.tinc_page_state.set_text({"preparing": "Подготовка…", "waiting": "Ожидание…", "connected": "Подключён",
            "reconnecting": "Переподключение…", "expired": "Доступ истёк", "failed": "Ошибка подключения"}.get(state, "Не подключён"))
        for style in ("connected", "checking", "danger"):
            self.tinc_page_state.remove_css_class(style)
        if state:
            self.tinc_page_state.add_css_class("connected" if state == "connected" else "danger" if state in ("expired", "failed") else "checking")
        mesh = self.render_tinc_mesh()
        if show_tinc:
            self.connect_button.set_visible(True)
            self.connect_button.set_label("Отключение…" if self.tinc_stopping else "Отключить" if self.tinc_worker_active else "Подключить")
            self.connect_button.set_sensitive((self.tinc_worker_active and self.vpn_process is not None and
                                               not self.tinc_stopping and not self.switching_route) or
                                              (not self.tinc_worker_active and self._vpn_worker_generation is None and
                                               self.tinc_can_connect() and not locked))
            self.connection_panel.set_visible(True)
            self.welcome_panel.set_visible(False)
            title = {"preparing": "Подготавливаем Tinc", "waiting": "Ждём готовности Tinc", "connected": "Tinc подключён",
                     "reconnecting": "Восстанавливаем Tinc", "expired": "Доступ к Tinc истёк", "failed": "Не удалось подключить Tinc"}.get(state,
                     "Tinc недоступен" if self.tinc_catalog_error else "Получаем страны Tinc" if self.tinc_catalog_loading else "Подключение через Tinc")
            self.connection_title.set_text(title)
            country = self.tinc_status.get("country") or self.tinc_country
            description = "Выход: " + country_name(country) + "." if country else "Выберите страну выхода."
            if self.tinc_worker_active and state == "connected":
                description += (f" Подключено входов: {mesh['connected']} из {len(mesh['nodes'])}." if mesh["complete"] and mesh["fresh"] else
                                f" Подключено входов: {mesh['connected']}." if mesh["fresh"] else " Состояние входов обновляется.")
            self.connection_description.set_text(description)
            if state == "connected" and self.connected_since is not None:
                self.update_connection_time()
            else:
                self.home_state.set_text(self.tinc_page_state.get_text())
            self.connection_hint.set_text("Интернет заблокирован до вашего действия" if state == "expired" else
                                          "Меш выбирает путь до назначенного выхода." if state == "connected" else
                                          self.tinc_hint())
            self.sidebar_state.set_text("VPN подключён" if state == "connected" else "Доступ истёк" if state == "expired" else "VPN отключён")
            for style in ("connected", "checking", "danger"):
                self.home_state.remove_css_class(style)
            if state:
                self.home_state.add_css_class("connected" if state == "connected" else "danger" if state in ("expired", "failed") else "checking")
            self.probe_button.set_sensitive(False)
            self.routes_probe_button.set_sensitive(False)
            self.listbox.set_sensitive(False)
            self.choose_button.set_sensitive(False)
            child = self.best_list.get_first_child()
            while child:
                child.set_sensitive(False)
                child = child.get_next_sibling()
        elif getattr(self, "_tinc_home_visible", False):
            self.update_route_card()
            self.connect_button.set_visible(bool(self.current_profiles))
            self.connection_title.set_text("VPN подключён" if self.connected_since is not None else
                                           "Готово к подключению" if not self.results_stale and self.selected_profile else "Проверьте маршруты")
            self.connection_description.set_text("Соединение установлено. Трафик проходит через выбранный маршрут." if self.connected_since is not None else
                                                 "Выберите проверенный маршрут для обычного подключения.")
        self._tinc_home_visible = show_tinc
