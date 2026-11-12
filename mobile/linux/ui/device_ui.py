"""Native account device management; all requests use the cabinet helper.

This page is opened explicitly. Merely listing devices or showing a link never
selects another device's subscription for this installation.
"""
import base64
import binascii
import subprocess
import threading
from urllib.parse import urlparse

import gi
gi.require_version("Gtk", "4.0")
gi.require_version("GdkPixbuf", "2.0")
from gi.repository import GdkPixbuf, Gio, GLib, Gtk, Pango

from desktop_ui import action_row, box, button, disclosure, field, icon_button, label, scrolled
from desktop_ui import heading as title_label


def format_usage(value):
    try:
        amount = max(0, int(value or 0))
    except (ValueError, TypeError):
        return "—"
    for unit in ("Б", "КБ", "МБ", "ГБ", "ТБ"):
        if amount < 1024 or unit == "ТБ":
            return f"{amount:.1f} {unit}" if unit != "Б" else f"{amount} Б"
        amount /= 1024


def device_presence(device):
    if device.get("blocked"):
        return "Заблокировано"
    if device.get("online"):
        return "В сети"
    if device.get("last_seen"):
        return "Было в сети: " + str(device["last_seen"])
    return "Ещё не подключалось"


class DeviceMixin:
    def build_devices_page(self):
        self._device_serial = 0
        self._devices_busy = False
        self._device_mutating = False
        self._device_mutations = set()
        self._devices_context = None
        self._devices_data = {}
        self._device_menus = []
        self._device_connection_actions = []
        page = box(16)
        header = box(12, True)
        header.append(icon_button("go-previous-symbolic", "Назад в кабинет", lambda _b: self.stack.set_visible_child_name("account")))
        title = title_label("Мои устройства")
        title.set_hexpand(True)
        header.append(title)
        self.devices_refresh_button = icon_button("view-refresh-symbolic", "Обновить список", self.open_devices)
        header.append(self.devices_refresh_button)
        page.append(header)
        page.append(label("Подключения вашей подписки Quantway.", "muted", True))
        self.device_add_button = button("Добавить устройство", self.add_account_device, "primary")
        self.device_buy_button = button("Докупить место", self.buy_device_place)
        self.device_buy_button.set_visible(False)
        self.device_actions = action_row(self.device_add_button, self.device_buy_button)
        page.append(self.device_actions)
        stats = Gtk.Grid(column_spacing=20, row_spacing=8)
        stats.add_css_class("device-ledger")
        self.device_ledger = stats
        self.devices_summary = label("—", "stat-value")
        self.devices_online = label("—", "stat-value")
        self.devices_traffic = label("—", "stat-value")
        for column, (caption, value) in enumerate((("Места", self.devices_summary), ("В сети", self.devices_online), ("Трафик за месяц", self.devices_traffic))):
            stat = box(4)
            stat.add_css_class("device-ledger-item")
            stat.set_hexpand(True)
            stat.append(label(caption, "stat-caption", True))
            stat.append(value)
            stats.attach(stat, column, 0, 1, 1)
        page.append(stats)
        self.devices_capacity_hint = label("", "caption", True)
        page.append(self.devices_capacity_hint)
        self.device_list = Gtk.ListBox()
        self.device_list.add_css_class("route-list")
        self.device_list.set_selection_mode(Gtk.SelectionMode.NONE)
        self.devices_empty = label("Устройства появятся после загрузки списка.", "muted", True)
        content = box(12)
        content.append(self.devices_empty)
        content.append(self.device_list)
        page.append(content)
        self._set_devices_busy(False)
        return scrolled(page)

    def open_devices(self, _button=None):
        if not self.session:
            self.stack.set_visible_child_name("account")
            self.status.set_text("Сначала войдите в аккаунт Quantway.")
            return
        if self.session.get("identity", {}).get("kind") == "owner":
            self.status.set_text("Устройства доступны в личном кабинете пользователя.")
            return
        context = self.account_context()
        if self._devices_context != context:
            self._device_serial += 1
            self._devices_context = context
            self._devices_busy = False
            self._devices_data = {}
            while child := self.device_list.get_first_child():
                self.device_list.remove(child)
            self.devices_empty.set_visible(True)
            self.devices_summary.set_text("—")
            self.devices_online.set_text("—")
            self.devices_traffic.set_text("—")
        self.stack.set_visible_child_name("devices")
        self.device_request("Обновляем список устройств…", "GET", "devices", None, self.render_devices)

    def _set_devices_busy(self, busy):
        self._devices_busy = busy
        self.devices_refresh_button.set_sensitive(not busy)
        data = self._devices_data
        available = bool(data.get("link_works") and int(data.get("in_use", 0)) < int(data.get("paid", 0)))
        full = int(data.get("in_use", 0)) >= int(data.get("paid", 0))
        buy = bool(full and data.get("buy") and (self.session or {}).get("identity", {}).get("kind") == "retail")
        self.device_add_button.set_visible(not buy)
        self.device_buy_button.set_visible(buy)
        self.device_add_button.set_sensitive(not busy and available)
        self.device_buy_button.set_sensitive(not busy)
        for control in (self.device_add_button, self.device_buy_button):
            control.remove_css_class("primary")
        if available:
            self.device_add_button.add_css_class("primary")
        elif buy:
            self.device_buy_button.add_css_class("primary")
        for menu in self._device_menus:
            menu.set_sensitive(not busy)
        for action in self._device_connection_actions:
            action.set_sensitive(not busy)

    def device_request(self, notice, method, path, body, done):
        context = self.account_context()
        if not context or self._devices_busy:
            return False
        if method != "GET" and getattr(self, "binding_busy", False):
            self.status.set_text("Дождитесь обновления кабинета и повторите действие.")
            return False
        parts = path.split("/")
        invalidates_link = (method == "DELETE" and len(parts) == 2 or
                            method == "POST" and len(parts) == 3 and parts[2] in ("block", "reissue"))
        source = getattr(self, "active_source", None) or {}
        active_target = len(parts) >= 2 and parts[0] == "devices" and source.get("kind") == "quantway" and source.get("id") == parts[1]
        if invalidates_link and active_target and (self.loading_profiles or self.probe_running or self.vpn_starting):
            self.status.set_text("Дождитесь завершения загрузки, проверки или подключения, затем повторите действие с этим устройством.")
            return False
        self._device_serial += 1
        serial = self._device_serial
        if method != "GET":
            self._device_mutations.add((context, serial))
            self._device_mutating = True
        self._set_devices_busy(True)
        self.sync_controls()
        self.status.set_text(notice)
        payload = {"cookie": context[1], "method": method, "path": path}
        if body is not None:
            payload["body"] = body
        threading.Thread(target=self._device_worker, args=(context, serial, payload, done), daemon=True).start()
        return True

    def _device_worker(self, context, serial, payload, done):
        try:
            data = self.call_helper("cabinet", payload)["data"]
            if not isinstance(data, dict):
                raise ValueError("Не удалось прочитать ответ сервиса.")
            error = ""
        except (OSError, subprocess.TimeoutExpired, ValueError, KeyError) as exc:
            data = {}
            error = str(exc).strip() if isinstance(exc, ValueError) else ""
            if not error or len(error) > 600:
                error = "Не удалось выполнить действие. Проверьте сеть и повторите попытку."
        GLib.idle_add(self._device_request_done, context, serial, data, error, done)

    def _device_request_done(self, context, serial, data, error, done):
        # A request can outlive logout or a page refresh. Release its own
        # mutation lock independently of whether its UI callback is still current.
        self._device_mutations.discard((context, serial))
        self._device_mutating = bool(self._device_mutations)
        self.sync_controls()
        if not self.account_callback_current(context) or serial != self._device_serial:
            self.load_pending_account_subscription()
            return False
        self._set_devices_busy(False)
        if error:
            self.status.set_text(error)
            self.load_pending_account_subscription()
            return False
        done(data)
        self.load_pending_account_subscription()
        return False

    def render_devices(self, data):
        self._devices_data = data
        self.update_account_stats(data)
        devices = data.get("devices") or []
        self.session["devices"] = devices
        self.devices_summary.set_text(f"{data.get('in_use', len(devices))} из {data.get('paid', 0)}")
        self.devices_online.set_text(str(data.get("online_count", 0)))
        self.devices_traffic.set_text(format_usage(data.get("month_bytes")))
        full = int(data.get("in_use", len(devices))) >= int(data.get("paid", 0))
        self.devices_capacity_hint.set_text("Подписка сейчас не действует. Продлите её, чтобы подключать устройства." if not data.get("link_works") else
                                           "Свободных мест нет. Удалите ненужное устройство или докупите место." if full and data.get("buy") else
                                           "Свободных мест нет. Удалите ненужное устройство, чтобы освободить место." if full else
                                           "Каждому устройству нужна своя ссылка на подписку.")
        refusal = data.get("refused")
        if refusal:
            self.devices_capacity_hint.set_text(f"Устройство «{refusal.get('device_name') or 'Без названия'}» не смогло подключиться: все места в сети заняты.")
        offer = data.get("buy")
        self.device_buy_button.set_visible(bool(offer) and self.session.get("identity", {}).get("kind") == "retail")
        if offer:
            self.device_buy_button.set_label("Докупить место за " + str(offer.get("price") or ""))
        while child := self.device_list.get_first_child():
            self.device_list.remove(child)
        self._device_menus = []
        self._device_connection_actions = []
        current_id = self.session.get("current_device")
        for device in sorted(devices, key=lambda item: item.get("id") != current_id):
            self.device_list.append(self._device_row(device, device.get("id") == current_id))
        self.devices_empty.set_text("Устройств пока нет. Добавьте устройство, чтобы получить ссылку.")
        self.devices_empty.set_visible(not devices)
        self._set_devices_busy(False)
        self.status.set_text("Список устройств обновлён.")

    def _device_row(self, device, current):
        row = Gtk.ListBoxRow()
        row.set_selectable(False)
        row.set_activatable(False)
        row.set_focusable(False)
        row.add_css_class("device-row")
        row.device = device
        content = box(10)
        heading = box(12, True)
        for side in ("top", "bottom"):
            getattr(content, "set_margin_" + side)(10)
        for side in ("start", "end"):
            getattr(content, "set_margin_" + side)(14)
        words = box(4)
        words.set_hexpand(True)
        name = str(device.get("name") or "Устройство")
        title = label(name, "route-label")
        title.set_ellipsize(Pango.EllipsizeMode.END)
        title.set_tooltip_text(name)
        title_row = box(8, True)
        title.set_hexpand(False)
        title_row.append(title)
        if current:
            badge = label("Этот компьютер", "device-current")
            badge.set_valign(Gtk.Align.CENTER)
            title_row.append(badge)
        words.append(title_row)
        platform = {"ios": "iPhone", "android": "Android", "windows": "Windows", "macos": "Mac", "linux": "Linux", "tv": "ТВ"}.get(device.get("platform"), "")
        presence = device_presence(device)
        status_row = box(8, True)
        status = label(presence, "device-status")
        status.add_css_class("status-pill")
        status.add_css_class("warning" if device.get("blocked") else "success" if device.get("online") else "neutral")
        status.set_ellipsize(Pango.EllipsizeMode.END)
        status.set_tooltip_text(presence)
        status_row.append(status)
        technical = " · ".join(dict.fromkeys(str(item) for item in (platform, device.get("kind")) if item and item != name))
        meta = label(technical, "device-meta")
        meta.set_ellipsize(Pango.EllipsizeMode.END)
        meta.set_hexpand(True)
        status_row.append(meta)
        tooltip = [presence, platform, "За месяц: " + format_usage(device.get("month_bytes"))]
        if device.get("kind"):
            tooltip.append(str(device["kind"]))
        if device.get("added"):
            tooltip.append("Добавлено: " + str(device["added"]))
        meta.set_tooltip_text("\n".join(tooltip))
        words.append(status_row)
        heading.append(words)
        usage = box(3)
        usage.set_valign(Gtk.Align.CENTER)
        amount = label(format_usage(device.get("month_bytes")), "device-traffic")
        amount.set_halign(Gtk.Align.END)
        usage.append(amount)
        caption = label("За месяц", "stat-caption")
        caption.set_halign(Gtk.Align.END)
        usage.append(caption)
        heading.append(usage)
        content.append(heading)
        row_actions = box(8, True)
        if not device.get("blocked"):
            connection = button("Ссылка и QR", lambda _b: self.show_device_connection(device))
            connection.add_css_class("device-connection-action")
            connection.set_tooltip_text("Открыть ссылку и QR-код для «" + name + "»")
            connection.update_property([Gtk.AccessibleProperty.LABEL], ["Подключение устройства «" + name + "»: ссылка и QR-код"])
            self._device_connection_actions.append(connection)
            row_actions.append(connection)
        spacer = box()
        spacer.set_hexpand(True)
        row_actions.append(spacer)
        menu = Gtk.MenuButton()
        menu.set_icon_name("view-more-symbolic")
        menu.set_valign(Gtk.Align.CENTER)
        menu.set_tooltip_text("Действия с устройством «" + name + "»")
        menu.update_property([Gtk.AccessibleProperty.LABEL], ["Действия с устройством «" + name + "»"])
        popover = Gtk.Popover()
        actions = box(5)
        actions.add_css_class("workspace")
        choices = []
        if not device.get("blocked"):
            choices += [("Использовать здесь", lambda _b: self.use_account_device(device))]
        choices += [("Переименовать", lambda _b: self.rename_account_device(device)),
                    ("Разблокировать" if device.get("blocked") else "Заблокировать", lambda _b: self.toggle_account_device(device))]
        if not device.get("blocked"):
            choices.append(("Перевыпустить ссылку", lambda _b: self.reissue_account_device(device)))
        choices.append(("Удалить устройство", lambda _b: self.delete_account_device(device)))
        for caption, callback in choices:
            item = button(caption, lambda b, handler=callback: (popover.popdown(), handler(b)), "flat")
            item.set_halign(Gtk.Align.FILL)
            if caption == "Удалить устройство":
                item.add_css_class("danger-action")
            actions.append(item)
        popover.set_child(actions)
        menu.set_popover(popover)
        self._device_menus.append(menu)
        row_actions.append(menu)
        content.append(row_actions)
        row.set_child(content)
        return row

    def _device_dialog(self, title, description="", width=480):
        dialog = Gtk.Dialog(title=title, transient_for=self.window, modal=True)
        dialog.add_css_class("quantway")
        dialog.set_default_size(width, -1)
        body = box(12)
        body.add_css_class("workspace")
        body.add_css_class("dialog-body")
        for side in ("top", "bottom", "start", "end"):
            getattr(body, "set_margin_" + side)(20)
        heading = label(title, "dialog-heading", True)
        heading.set_wrap_mode(Pango.WrapMode.WORD_CHAR)
        body.append(heading)
        if description:
            description_label = label(description, "muted", True)
            description_label.set_wrap_mode(Pango.WrapMode.WORD_CHAR)
            description_label.set_max_width_chars(45)
            body.append(description_label)
        viewport = scrolled(body)
        viewport.set_propagate_natural_height(True)
        parent_height = self.window.get_height() or self.window.get_default_size()[1]
        viewport.set_max_content_height(max(220, parent_height - 160))
        dialog.get_content_area().append(viewport)
        return dialog, body

    @staticmethod
    def _device_response_button(dialog, caption, response, style=None):
        item = dialog.add_button(caption, response)
        # Scope the native action area, leaving window controls native.
        area = item.get_parent()
        area.add_css_class("workspace")
        area.set_margin_start(20)
        area.set_margin_end(20)
        area.set_margin_bottom(16)
        if isinstance(area, Gtk.Box):
            area.set_spacing(8)
        if style:
            item.add_css_class(style)
        return item

    def _device_confirm(self, title, description, accept, action):
        context = self.account_context()
        if not context or self._devices_busy:
            return
        dialog, body = self._device_dialog(title)
        warning = label(description, "status-pill", True)
        warning.add_css_class("warning")
        warning.set_wrap_mode(Pango.WrapMode.WORD_CHAR)
        body.append(warning)
        destructive = accept in ("Удалить", "Заблокировать", "Перевыпустить")
        self._device_response_button(dialog, "Отмена", Gtk.ResponseType.CANCEL)
        self._device_response_button(dialog, accept, Gtk.ResponseType.ACCEPT, "danger-action" if destructive else "primary")
        dialog.set_default_response(Gtk.ResponseType.CANCEL)
        def respond(window, response):
            window.destroy()
            if response == Gtk.ResponseType.ACCEPT and self.account_callback_current(context):
                action()
        dialog.connect("response", respond)
        dialog.present()
        return dialog

    def add_account_device(self, _button=None):
        self.device_request("Загружаем список систем…", "GET", "platforms", None, self._new_device_dialog)

    @staticmethod
    def _device_name_field(entry):
        group = box(6)
        heading = box(8, True)
        caption = Gtk.Label.new_with_mnemonic("_Название")
        caption.set_xalign(0)
        caption.add_css_class("caption")
        caption.set_mnemonic_widget(entry)
        caption.set_hexpand(True)
        heading.append(caption)
        count = label(f"{len(entry.get_text())} / 40 символов", "caption")
        heading.append(count)
        group.append(heading)
        group.append(entry)
        return group, count

    def _new_device_dialog(self, data):
        context = self.account_context()
        platforms = [{"id": "", "label": "Другое устройство"}] + list(data.get("platforms") or [])
        dialog, body = self._device_dialog("Добавить устройство", "Для устройства появится отдельная ссылка. Выберите систему или задайте название.")
        platform = Gtk.DropDown.new_from_strings([str(item.get("label") or "Устройство") for item in platforms])
        platform.update_property([Gtk.AccessibleProperty.LABEL], ["Система устройства"])
        body.append(field("_Система", platform))
        name = Gtk.Entry(max_length=40)
        name.set_placeholder_text("Например, телевизор")
        name_field, count = self._device_name_field(name)
        body.append(name_field)
        validation = label("Введите название или выберите систему.", "caption", True)
        body.append(validation)
        self._device_response_button(dialog, "Отмена", Gtk.ResponseType.CANCEL)
        submit = self._device_response_button(dialog, "Добавить", Gtk.ResponseType.ACCEPT, "primary")
        submit.set_sensitive(False)
        def changed(*_args):
            valid = bool(name.get_text().strip()) or platform.get_selected() > 0
            submit.set_sensitive(valid)
            count.set_text(f"{len(name.get_text())} / 40 символов")
            validation.set_text("Название подберётся автоматически." if not name.get_text().strip() and platform.get_selected() > 0 else
                                "Название поможет найти устройство в списке." if valid else "Введите название или выберите систему.")
        name.connect("changed", changed)
        platform.connect("notify::selected", changed)
        name.connect("activate", lambda _entry: dialog.response(Gtk.ResponseType.ACCEPT) if submit.get_sensitive() else None)
        def respond(window, response):
            if response == Gtk.ResponseType.ACCEPT and not submit.get_sensitive():
                return
            selected = platforms[platform.get_selected()]
            payload = {"name": name.get_text().strip()}
            if selected.get("id"):
                payload["platform"] = selected["id"]
            window.destroy()
            if response != Gtk.ResponseType.ACCEPT or not self.account_callback_current(context):
                return
            self.device_request("Добавляем устройство…", "POST", "devices", payload,
                                lambda result: self._device_changed("create", {}, result))
        dialog.connect("response", respond)
        dialog.present()
        name.grab_focus()
        return dialog

    def rename_account_device(self, device):
        context = self.account_context()
        dialog, body = self._device_dialog("Название устройства", "Ссылка устройства останется прежней.")
        entry = Gtk.Entry(max_length=40)
        entry.set_text(str(device.get("name") or ""))
        name_field, count = self._device_name_field(entry)
        body.append(name_field)
        validation = label("Название не должно быть пустым.", "caption", True)
        body.append(validation)
        self._device_response_button(dialog, "Отмена", Gtk.ResponseType.CANCEL)
        submit = self._device_response_button(dialog, "Сохранить", Gtk.ResponseType.ACCEPT, "primary")
        submit.set_sensitive(bool(entry.get_text().strip()))
        def changed(item):
            submit.set_sensitive(bool(item.get_text().strip()))
            count.set_text(f"{len(item.get_text())} / 40 символов")
        entry.connect("changed", changed)
        entry.connect("activate", lambda _entry: dialog.response(Gtk.ResponseType.ACCEPT) if submit.get_sensitive() else None)
        def respond(window, response):
            value = entry.get_text().strip()
            if response == Gtk.ResponseType.ACCEPT and not value:
                return
            window.destroy()
            if response == Gtk.ResponseType.ACCEPT and self.account_callback_current(context):
                self.device_request("Сохраняем название…", "PATCH", f"devices/{device['id']}", {"name": value},
                                    lambda result: self._device_changed("rename", device, result))
        dialog.connect("response", respond)
        dialog.present()
        entry.grab_focus()
        entry.select_region(0, -1)
        return dialog

    def toggle_account_device(self, device):
        action = "unblock" if device.get("blocked") else "block"
        perform = lambda: self.device_request("Обновляем устройство…", "POST", f"devices/{device['id']}/{action}", {},
                                              lambda result: self._device_changed(action, device, result))
        if action == "unblock":
            perform()
            return
        name = device.get("name") or "Устройство"
        return self._device_confirm("Заблокировать устройство?", f"«{name}» перестанет подключаться. Его место сохранится; устройство можно разблокировать здесь же.", "Заблокировать", perform)

    def delete_account_device(self, device):
        name = device.get("name") or "Устройство"
        return self._device_confirm("Удалить устройство?", f"Ссылка устройства «{name}» перестанет работать, место освободится. Восстановить эту ссылку будет нельзя.", "Удалить",
                                    lambda: self.device_request("Удаляем устройство…", "DELETE", f"devices/{device['id']}", None,
                                                                lambda result: self._device_changed("delete", device, result)))

    def reissue_account_device(self, device):
        name = device.get("name") or "Устройство"
        return self._device_confirm("Перевыпустить ссылку?", f"Старая ссылка устройства «{name}» сразу перестанет работать. Появятся новая ссылка и QR-код, которые нужно заново добавить на устройстве.", "Перевыпустить",
                                    lambda: self.device_request("Перевыпускаем ссылку…", "POST", f"devices/{device['id']}/reissue", {},
                                                                lambda result: self._device_changed("reissue", device, result)))

    def _device_changed(self, action, previous, data):
        current = bool(previous.get("id") and previous.get("id") == self.session.get("current_device"))
        old_link = self.session.get("device_link")
        reload_profile = bool(current and old_link and self.loaded_link == old_link)
        fresh = data.get("device") or {}
        if current and action == "delete":
            self.session["current_device"] = ""
            self.session["device_link"] = ""
            self.session["device_error"] = "Этот компьютер удалён из аккаунта. Обновите кабинет, чтобы подключить его снова."
        elif current and action == "block":
            self.session["device_error"] = "Этот компьютер заблокирован. Разблокируйте его в списке устройств."
        elif current and action == "reissue":
            self.session["current_device"] = fresh.get("id", "")
            self.session["device_link"] = ""
            self.session["device_error"] = ""
        elif current and action == "unblock":
            self.session["device_error"] = ""
        hook = getattr(self, "on_account_device_changed", None)
        if hook:
            decision = hook(action, data, previous)
            if isinstance(decision, bool):
                reload_profile = decision
        elif current and hasattr(self, "on_current_device_changed"):
            decision = self.on_current_device_changed(action, data, previous)
            if isinstance(decision, bool):
                reload_profile = decision
        message = data.get("message") or {"rename": "Название сохранено.", "block": "Устройство заблокировано.",
                                           "unblock": "Устройство разблокировано.", "delete": "Устройство удалено.",
                                           "create": "Устройство добавлено.", "reissue": "Новая ссылка готова."}.get(action, "Готово.")
        if data.get("binding_warning"):
            message += " " + str(data["binding_warning"])
        def updated(devices):
            self.render_devices(devices)
            self.status.set_text(message)
            if action in ("create", "reissue") and fresh.get("id") and not fresh.get("blocked"):
                self.show_device_connection(fresh, activate=reload_profile and action == "reissue")
        self.device_request("Обновляем список устройств…", "GET", "devices", None, updated)

    def show_device_connection(self, device, activate=False):
        self.device_request("Получаем ссылку устройства…", "GET", f"devices/{device['id']}/connect", None,
                            lambda data: self._show_device_connection(device, data, activate))

    def _show_device_connection(self, device, data, activate=False):
        link = data.get("sub_url") or ""
        if not self._safe_https(link) or urlparse(link).fragment:
            self.status.set_text("Не удалось получить корректную ссылку устройства.")
            return
        context = self.account_context()
        current = device.get("id") == self.session.get("current_device")
        if current:
            self.session["device_link"] = link
            if activate:
                self.queue_account_subscription(link, context)
        dialog, body = self._device_dialog("Подключение устройства", width=600)
        content = box(16)
        name = label(str(device.get("name") or "Устройство"), "section-title", True)
        name.set_wrap_mode(Pango.WrapMode.WORD_CHAR)
        content.append(name)
        content.append(label("Отсканируйте QR-код в VPN-приложении или скопируйте ссылку.", "muted", True))
        connection = box(16, True)
        picture = self._device_qr(data.get("qr") or "")
        if picture:
            qr = box(8, style="qr-panel")
            picture.set_halign(Gtk.Align.CENTER)
            picture.update_property([Gtk.AccessibleProperty.LABEL], ["QR-код подписки устройства"])
            qr.set_valign(Gtk.Align.START)
            qr.append(picture)
            connection.append(qr)
        controls = box(12)
        controls.set_hexpand(True)
        entry = Gtk.Entry()
        entry.set_width_chars(12)
        entry.set_text(link)
        entry.set_visibility(False)
        entry.set_editable(False)
        controls.append(field("_Ссылка на подписку", entry))
        reveal = Gtk.CheckButton(label="Показать ссылку")
        reveal.connect("toggled", lambda check: entry.set_visibility(check.get_active()))
        controls.append(reveal)
        copy = button("Копировать ссылку", lambda _b: self._copy_device_link(link), "primary")
        copy.set_halign(Gtk.Align.FILL)
        controls.append(copy)
        use = button("Использовать здесь", lambda _b: self._use_device_link_dialog(dialog, device, link, context))
        use.set_halign(Gtk.Align.FILL)
        controls.append(use)
        connection.append(controls)
        content.append(connection)
        platforms = data.get("platforms") or []
        if platforms:
            content.append(self._device_guides(platforms, device.get("platform")))
        body.append(content)
        self._device_response_button(dialog, "Закрыть", Gtk.ResponseType.CLOSE)
        dialog.connect("response", lambda window, _response: window.destroy())
        dialog.present()
        self.status.set_text("Ссылка устройства готова.")
        return dialog

    def _device_guides(self, platforms, preferred_platform):
        content = box(12)
        system = Gtk.DropDown.new_from_strings([str(item.get("label") or "Система") for item in platforms])
        system.update_property([Gtk.AccessibleProperty.LABEL], ["Система устройства"])
        client = Gtk.DropDown()
        client.update_property([Gtk.AccessibleProperty.LABEL], ["VPN-приложение"])
        content.append(field("_Система устройства", system))
        content.append(field("_VPN-приложение", client))
        steps = box(10)
        content.append(steps)
        choices = {"apps": []}

        def show_client(*_args):
            while child := steps.get_first_child():
                steps.remove(child)
            index = client.get_selected()
            apps = choices["apps"]
            if index >= len(apps):
                steps.append(label("Используйте ссылку в VPN-приложении на этом устройстве.", "muted", True))
                return
            application = apps[index]
            platform = platforms[system.get_selected()]
            install = application.get("install_url") or ""
            if self._safe_https(install):
                steps.append(button("Установить приложение", lambda _b, url=install: self._device_open_url(url), "primary"))
            for text in (application.get("note"), platform.get("how")):
                if text:
                    note = label(str(text), "caption", True)
                    note.set_wrap_mode(Pango.WrapMode.WORD_CHAR)
                    steps.append(note)
            guide = application.get("guide") or {}
            for key, heading in (("import", "Добавить подписку"), ("enable", "Подключиться")):
                if guide.get(key):
                    steps.append(label(heading, "stat-caption"))
                for number, text in enumerate(guide.get(key) or [], 1):
                    step = label(f"{number}. {text}", wrap=True)
                    step.set_wrap_mode(Pango.WrapMode.WORD_CHAR)
                    steps.append(step)

        def show_system(*_args):
            index = system.get_selected()
            if index >= len(platforms):
                return
            choices["apps"] = platforms[index].get("apps") or []
            client.set_model(Gtk.StringList.new([str(item.get("name") or "Приложение") for item in choices["apps"]]))
            client.set_selected(0)
            show_client()

        system.connect("notify::selected", show_system)
        client.connect("notify::selected", show_client)
        for index, item in enumerate(platforms):
            if item.get("id") == preferred_platform:
                system.set_selected(index)
                break
        show_system()
        return disclosure("Инструкции подключения", content)

    @staticmethod
    def _safe_https(value):
        try:
            parsed = urlparse(value)
            return parsed.scheme == "https" and bool(parsed.hostname) and not parsed.username and not parsed.password
        except ValueError:
            return False

    def _device_open_url(self, url):
        if not self._safe_https(url):
            return
        try:
            Gio.AppInfo.launch_default_for_uri(url, None)
        except GLib.Error:
            self.status.set_text("Не удалось открыть браузер. Попробуйте ещё раз.")

    def _copy_device_link(self, link):
        self.window.get_display().get_clipboard().set(link)
        self.status.set_text("Ссылка скопирована.")

    @staticmethod
    def _device_qr(value):
        prefix = "data:image/svg+xml;base64,"
        if not value.startswith(prefix) or len(value) > 1024 * 1024:
            return None
        try:
            raw = base64.b64decode(value[len(prefix):], validate=True)
            stream = Gio.MemoryInputStream.new_from_bytes(GLib.Bytes.new(raw))
            pixbuf = GdkPixbuf.Pixbuf.new_from_stream_at_scale(stream, 220, 220, True, None)
            picture = Gtk.Picture.new_for_pixbuf(pixbuf)
            picture.set_can_shrink(True)
            picture.set_size_request(220, 220)
            if hasattr(picture, "set_content_fit"):
                picture.set_content_fit(Gtk.ContentFit.CONTAIN)
            else:
                picture.set_keep_aspect_ratio(True)
            return picture
        except (ValueError, binascii.Error, GLib.Error):
            return None

    def use_account_device(self, device):
        self.device_request("Получаем ссылку устройства…", "GET", f"devices/{device['id']}/connect", None,
                            lambda data: self._confirm_device_use(device, data.get("sub_url") or "", self.account_context()))

    def _use_device_link_dialog(self, dialog, device, link, context):
        dialog.destroy()
        self._confirm_device_use(device, link, context)

    def _confirm_device_use(self, device, link, context):
        if not self.account_callback_current(context) or not self._safe_https(link):
            return
        action = lambda: self._apply_device_link(device, link, context)
        if device.get("id") == self.session.get("current_device"):
            action()
        else:
            return self._device_confirm("Использовать эту ссылку здесь?", "Если ссылка уже используется на другом устройстве, возможен конфликт подключений. Для каждого устройства лучше использовать отдельную ссылку.", "Использовать", action)

    def _apply_device_link(self, device, link, context):
        if not self.account_callback_current(context):
            return
        if device.get("id") == self.session.get("current_device"):
            self.session["device_link"] = link
        hook = getattr(self, "on_device_connection_selected", None)
        if hook:
            hook(device, link, context)
        else:
            self.queue_account_subscription(link, context)

    def buy_device_place(self, _button=None):
        if not self._devices_data.get("buy"):
            return
        self.device_request("Загружаем способы оплаты…", "GET", "plans", None, self._device_place_dialog)

    def _device_place_dialog(self, data):
        context = self.account_context()
        methods = data.get("methods") or []
        offer = self._devices_data.get("buy")
        if not methods or not offer:
            self.status.set_text("Докупить место сейчас нельзя. Обновите список устройств.")
            return
        dialog, body = self._device_dialog("Ещё одно место", "Одно дополнительное устройство в вашей подписке.")
        price = box(4, style="header-stats")
        price.append(label(str(offer.get("price") or "—"), "stat-value"))
        price.append(label("Доплата до конца текущей подписки", "stat-caption", True))
        body.append(price)
        body.append(label("Дальше — " + str(offer.get("monthly") or "вместе с подпиской") + ". Место продлевается вместе с подпиской.", "muted", True))
        body.append(label(f"Можно докупить ещё {offer.get('left', 0)}. Доступ появится после оплаты.", "caption", True))
        method = Gtk.DropDown.new_from_strings([str(item.get("label") or "Способ оплаты") for item in methods])
        method.update_property([Gtk.AccessibleProperty.LABEL], ["Способ оплаты"])
        body.append(field("_Способ оплаты", method))
        self._device_response_button(dialog, "Отмена", Gtk.ResponseType.CANCEL)
        self._device_response_button(dialog, "Создать счёт", Gtk.ResponseType.ACCEPT, "primary")
        def respond(window, response):
            selected = method.get_selected()
            window.destroy()
            if response != Gtk.ResponseType.ACCEPT or not self.account_callback_current(context) or selected >= len(methods):
                return
            self.device_request("Создаём счёт на дополнительное место…", "POST", "invoices", {"plan": "device", "method": methods[selected].get("id")}, self._device_invoice_created)
        dialog.connect("response", respond)
        dialog.present()
        return dialog

    def _device_invoice_created(self, data):
        hook = getattr(self, "on_device_invoice", None)
        if hook:
            hook(data)
        else:
            self.status.set_text("Счёт создан. Откройте его в разделе «Счета и оплаты» кабинета.")
