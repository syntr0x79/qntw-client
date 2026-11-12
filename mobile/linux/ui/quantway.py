#!/usr/bin/env python3
"""GTK desktop shell; credentials pass only via the local helper's stdin."""
import json
import subprocess
import threading
import time

import gi
gi.require_version("Gtk", "4.0")
gi.require_version("Gdk", "4.0")
gi.require_version("GdkPixbuf", "2.0")
from gi.repository import Gdk, GdkPixbuf, Gio, GLib, Gtk, Pango

from desktop_ui import build_window, box, button, field, label
from cabinet_ui import CabinetMixin
from device_ui import DeviceMixin
from external_ui import ExternalMixin
from account_stats import AccountStatsMixin
from auth_persistence import AuthPersistenceMixin, ServiceFailure
from tinc_ui import TincMixin
from platform_runtime import helper_path, helper_process_options


class Quantway(TincMixin, AuthPersistenceMixin, AccountStatsMixin, CabinetMixin, DeviceMixin, ExternalMixin, Gtk.Application):
    def __init__(self):
        super().__init__(application_id="link.quantway.desktop", flags=Gio.ApplicationFlags(0))
        self.window = None
        self.session = None
        self.account_generation = 0
        self.renewal_generation = 0
        self.pending_account_subscription = None
        self.binding_busy = False
        self.current_profiles = []
        self.selected_profile = None
        self.loaded_link = ""
        self.probe_running = False
        self.probe_process = None
        self.probe_cancelled = False
        self.subscription_generation = 0
        self._probe_generation = None
        self._probe_cancel = None
        self._vpn_worker_generation = None
        self.vpn_process = None
        self.vpn_starting = False
        self.selection_automatic = True
        self.connected_since = None
        self.connection_timer = None
        self.loading_profiles = False
        self.results_stale = True
        self.last_checked = None
        self._plan_choices = None
        self.closing = False
        self._layout_width = None
        self.active_source = None
        self.fetch_source = None
        self.pending_route_switch = None
        self.route_switch_dialog = None
        self.switching_route = False
        self.vpn_generation = 0
        self.restore_route_selection = False
        self.init_external_state()
        self.init_cabinet_state()
        self.init_account_stats()
        self.init_auth_persistence()
        self.init_tinc_state()

    def do_activate(self):
        if self.window:
            self.window.present()
            return
        build_window(self)
        self.stack.add_named(self.build_devices_page(), "devices")
        self.build_cabinet_pages()
        self.stack.connect("notify::visible-child-name", self.account_stats_page_changed)
        self.sync_controls()
        self.start_session_restore()

    def navigate(self, button, page):
        if button.get_active():
            if page == "tinc":
                self.open_tinc_page()
            else:
                self.stack.set_visible_child_name(page)

    def update_responsive_layout(self, _widget, _clock):
        width = self.stack.get_width()
        window_width = self.window.get_width()
        settings = self.window.get_settings()
        key = (width, window_width, self.stack.get_height(), self.home_state.get_text(),
               self.connection_hint.get_text(), self.tinc_country_field.get_visible(),
               self.route_preview_title.get_text(), self.route_apply_button.get_label(),
               settings.get_property("gtk-font-name"), settings.get_property("gtk-xft-dpi"))
        if width > 0 and key != self._layout_width:
            self._layout_width = key
            # Collapse before the wide rail itself becomes the window's
            # minimum width. Pango measures text even while its label is hidden.
            sidebar_min = max(180, self.brand_wordmark.get_layout().get_pixel_size()[0] + 77,
                              max(item.get_layout().get_pixel_size()[0] for item in self.nav_labels) + 74)
            compact = window_width < sidebar_min + 480 + 48
            self.sidebar.set_size_request(70 if compact else 180, -1)
            self.brand_wordmark.set_visible(not compact)
            self.sidebar_foot.set_visible(not compact)
            self.sidebar_state.set_visible(not compact)
            for item in self.nav_labels:
                item.set_visible(not compact)
            self.home_columns.set_orientation(Gtk.Orientation.VERTICAL)
            self.home_columns.set_homogeneous(False)
            if hasattr(self, "plan_columns"):
                panels = [self.plan_columns.get_first_child(), self.plan_columns.get_last_child()]
                minimum = sum(panel.measure(Gtk.Orientation.HORIZONTAL, -1)[0] for panel in panels) + 14
                wide = width >= 680 and minimum <= width
                self.plan_columns.set_orientation(Gtk.Orientation.HORIZONTAL if wide else Gtk.Orientation.VERTICAL)
                self.plan_columns.set_homogeneous(wide)
            if hasattr(self, "account_card_columns"):
                panels = [self.account_card_columns.get_first_child(), self.account_card_columns.get_last_child()]
                minimum = sum(panel.measure(Gtk.Orientation.HORIZONTAL, -1)[0] for panel in panels if panel.get_visible()) + 24
                wide = width >= 680 and minimum <= width
                self.account_card_columns.set_orientation(Gtk.Orientation.HORIZONTAL if wide else Gtk.Orientation.VERTICAL)
                self.account_card_columns.set_homogeneous(wide)
                if wide:
                    self.account_stats_panel.remove_css_class("stacked")
                else:
                    self.account_stats_panel.add_css_class("stacked")
            minimum = (self.home_heading.get_first_child().measure(Gtk.Orientation.HORIZONTAL, -1)[0] +
                       self.home_state.measure(Gtk.Orientation.HORIZONTAL, -1)[0] + 12)
            horizontal = minimum <= width
            self.home_heading.set_orientation(Gtk.Orientation.HORIZONTAL if horizontal else Gtk.Orientation.VERTICAL)
            self.home_state.set_halign(Gtk.Align.END if horizontal else Gtk.Align.START)
            header_minimum = sum(child.measure(Gtk.Orientation.HORIZONTAL, -1)[0] for child in
                                 (self.routes_header.get_first_child(), self.routes_probe_button)) + 10
            self.routes_header.set_orientation(Gtk.Orientation.HORIZONTAL if header_minimum <= width else Gtk.Orientation.VERTICAL)
            self.routes_probe_button.set_halign(Gtk.Align.START)
            filter_minimum = sum(item.measure(Gtk.Orientation.HORIZONTAL, -1)[0] for item in
                                 (self.route_search, self.route_available)) + 10
            self.route_filters.set_orientation(Gtk.Orientation.HORIZONTAL if filter_minimum <= width else Gtk.Orientation.VERTICAL)
            preview_minimum = (self.route_preview_words.measure(Gtk.Orientation.HORIZONTAL, -1)[1] +
                               self.route_apply_button.measure(Gtk.Orientation.HORIZONTAL, -1)[0] + 12)
            self.route_preview_box.set_orientation(Gtk.Orientation.HORIZONTAL if preview_minimum <= width else Gtk.Orientation.VERTICAL)
            # A desktop form follows its content instead of stretching short
            # controls across a wide panel. Large text uses the same widgets
            # on separate rows when their measured widths no longer fit.
            form_width = min(width, 720)
            mode_minimum = self.tinc_mode_field.measure(Gtk.Orientation.HORIZONTAL, -1)[0]
            country_minimum = self.tinc_country_field.measure(Gtk.Orientation.HORIZONTAL, -1)[0]
            inline = not self.tinc_country_field.get_visible() or mode_minimum + country_minimum + 16 <= form_width - 30
            self.tinc_controls.set_orientation(Gtk.Orientation.HORIZONTAL if inline else Gtk.Orientation.VERTICAL)
            self.tinc_controls.set_spacing(16 if inline else 10)
            metrics = self.connection_hint.get_pango_context().get_metrics(None, None)
            hint_width = min(self.connection_hint.measure(Gtk.Orientation.HORIZONTAL, -1)[1],
                             metrics.get_approximate_char_width() / Pango.SCALE * 24)
            visible_actions = [item for item in (self.connect_button, self.add_subscription_button) if item.get_visible()]
            action_width = max((item.measure(Gtk.Orientation.HORIZONTAL, -1)[0] for item in visible_actions), default=0)
            inline_action = action_width + hint_width + 12 <= form_width
            self.connection_actions.set_orientation(Gtk.Orientation.HORIZONTAL if inline_action else Gtk.Orientation.VERTICAL)
            for item in (self.connect_button, self.add_subscription_button):
                item.set_halign(Gtk.Align.END if inline_action else Gtk.Align.START)
            end_inset = max(0, width - 720)
            for item in (self.connection_body, self.connection_actions):
                item.set_margin_start(0)
                item.set_margin_end(end_inset)
            # Let the action follow a short form. On a short/zoomed window only
            # the settings scroll, leaving Connect/Stop in the visible area.
            footer_height = self.connection_actions.measure(Gtk.Orientation.VERTICAL, max(1, form_width))[0]
            self.connection_scroll.set_max_content_height(max(100, self.stack.get_height() - footer_height))
        return not self.closing

    def update_notice(self, *_args):
        text = self.status.get_text()
        critical = self.tinc_worker_active and self.tinc_status.get("status") == "expired"
        capacity = self.tinc_worker_active and self.tinc_status.get("code") == "capacity_exhausted"
        catalog_problem = (self.connection_mode == "tinc" and not self.tinc_worker_active and
                           bool(self.tinc_catalog_error) and self.stack.get_visible_child_name() in ("home", "tinc"))
        display = (self.tinc_hint() if critical else self.tinc_catalog_error if catalog_problem else
                   text if text.strip() else self.tinc_hint() if capacity else self.current_hint())
        kind = self.status.get_kind()
        if critical:
            kind = "error"
        elif catalog_problem:
            kind = "warning" if "все подключения заняты" in display.lower() else "error"
        elif capacity or "все подключения заняты" in display.lower():
            kind = "warning"
        elif kind is None:
            kind = "error" if any(word in display.lower() for word in
                ("не удалось", "не найдены", "нет свободных", "недоступ", "заблокирован", "ограничен", "недействител", "ошибка")) else "info"
        routine = text.startswith(("Проверка завершена.", "Выбран маршрут:", "Кабинет открыт", "Кабинет обновлён", "Список устройств обновлён", "VPN отключён."))
        visible = critical or capacity or catalog_problem or (bool(text.strip()) and not routine)
        self.notice.set_visible(visible)
        self.notice_info.set_visible(kind != "error")
        self.notice_error.set_visible(kind == "error")
        self.notice_text = self.notice_error if kind == "error" else self.notice_info
        self.notice_text.set_text(" ".join(display.split()))
        self.notice_text.set_tooltip_text(display)
        self._notice_full_text = display
        self.notice_details.update_property([Gtk.AccessibleProperty.LABEL], ["Прочитать сообщение целиком: " + display])
        self.notice_dismiss.set_visible(not critical)
        for style in ("error", "warning"):
            self.notice.remove_css_class(style)
        if kind in ("error", "warning"):
            self.notice.add_css_class(kind)
        self.notice_icon.set_from_icon_name("dialog-warning-symbolic" if kind in ("error", "warning") else "dialog-information-symbolic")

    def show_notice_details(self, _button=None):
        dialog = Gtk.Dialog(title="Сообщение Quantway", transient_for=self.window, modal=True, destroy_with_parent=True)
        dialog.add_css_class("quantway")
        dialog.set_default_size(560, 340)
        body = dialog.get_content_area()
        body.add_css_class("workspace")
        for side in ("top", "bottom", "start", "end"):
            getattr(body, "set_margin_" + side)(20)
        message = label(getattr(self, "_notice_full_text", self.status.get_text()), wrap=True)
        message.set_selectable(True)
        message.update_property([Gtk.AccessibleProperty.LABEL], [message.get_text()])
        scroll = Gtk.ScrolledWindow()
        scroll.set_policy(Gtk.PolicyType.NEVER, Gtk.PolicyType.AUTOMATIC)
        scroll.set_min_content_height(180)
        scroll.set_vexpand(True)
        scroll.set_child(message)
        body.append(scroll)
        close = dialog.add_button("Закрыть", Gtk.ResponseType.CLOSE)
        close.get_parent().add_css_class("workspace")
        dialog.connect("response", lambda window, _response: window.destroy())
        self.notice_dialog = dialog
        dialog.present()
        close.grab_focus()

    def render_source_choices(self):
        choices = []
        session = self.session or {}
        if session.get("current_device") and session.get("device_link"):
            choices.append(("quantway", session["current_device"], "Quantway — этот компьютер", session["device_link"]))
        choices.extend(("external", item["id"], item["name"], item["link"]) for item in self.external_subscriptions)
        active = self.active_source or {}
        key = tuple(choices), active.get("kind"), active.get("id")
        self.home_source_name.set_text(active.get("name") or "Текущая подписка")
        if key != getattr(self, "_source_choices_key", None):
            self._source_choices_key = key
            while child := self.source_choices.get_first_child():
                self.source_choices.remove(child)
            for kind, source_id, name, link in choices:
                selected = active.get("kind") == kind and active.get("id") == source_id
                item = button(name + (" — выбрана" if selected else ""),
                              lambda _b, k=kind, sid=source_id, n=name, value=link: self.choose_subscription_source(k, sid, n, value), "flat")
                item.set_halign(Gtk.Align.FILL)
                item.get_child().set_wrap_mode(Pango.WrapMode.WORD_CHAR)
                item.set_sensitive(not selected)
                self.source_choices.append(item)
            self.source_choices.append(button("Добавить другую подписку", lambda _b: self.open_subscription_sources(), "flat"))

    def choose_subscription_source(self, kind, source_id, name, link):
        self.source_popover.popdown()
        if not self.external_busy() and self._vpn_worker_generation is None:
            self.select_subscription(link, kind, source_id, name)

    def open_subscription_sources(self):
        self.source_popover.popdown()
        self.nav_by_page["external"].set_active(True)

    def current_hint(self):
        page = self.stack.get_visible_child_name()
        if page == "support-chat":
            return "Здесь можно написать команде поддержки и прочитать её ответы."
        if page in ("renew", "cabinet-section", "devices"):
            if page == "devices":
                return "Управляйте устройствами аккаунта. Изменения применяются к их VPN-ссылкам."
            if page == "cabinet-section":
                return "Здесь доступны действия вашего кабинета Quantway."
            return "Выберите тариф и способ оплаты для продления подписки."
        if page == "external":
            return "Добавьте подписку другого провайдера. Она не связана с устройствами аккаунта Quantway."
        if page == "account":
            if not self.session:
                return "Введите код входа в Quantway. Внешние провайдеры находятся в разделе «Другие VPN»."
            if self.session.get("needs_replacement"):
                return "Освободите место или выберите устройство для замены."
            return "Здесь можно продлить подписку и обновить данные кабинета."
        if page == "tinc" or self.tinc_worker_active or self.connection_mode == "tinc":
            return self.tinc_hint()
        if self.vpn_starting:
            return "Подтвердите системный запрос, если он появится."
        if self.vpn_process is not None or self.connected_since is not None:
            return "Проверенные маршруты можно переключать с подтверждением. Для новой проверки отключите VPN."
        if self.probe_running:
            return "Проверяем маршруты в вашей сети и выберем самый быстрый."
        if self.loading_profiles:
            return "Загружаем подписку. После загрузки проверим маршруты."
        if self.selected_profile and not self.results_stale:
            return "Можно подключаться или выбрать другой рабочий маршрут."
        if self.current_profiles:
            return "Проверьте маршруты перед подключением."
        return "Войдите в Quantway или подключите внешнюю подписку."

    def sync_probe_button(self, button, _property):
        self.routes_probe_button.set_label(button.get_label())
        self.routes_probe_button.set_sensitive(button.get_sensitive())

    def sync_controls(self):
        if not self.window or self.closing:
            return
        active = (self.vpn_process is not None or self.vpn_starting or self.connected_since is not None or
                  self._vpn_worker_generation is not None or self.tinc_worker_active)
        mutating = getattr(self, "_device_mutating", False)
        busy = active or self.probe_running or self.loading_profiles or self.switching_route or mutating
        selecting = self.vpn_starting or self.probe_running or self.loading_profiles or self.switching_route or mutating
        matches = bool(self.loaded_link) and self.entry.get_text().strip() == self.loaded_link
        valid = bool(self.selected_profile and self.selected_profile.get("job")) and not self.results_stale
        self.probe_button.set_sensitive(matches and bool(self.current_profiles) and not active and not self.loading_profiles and not mutating)
        if self.probe_cancelled and self.probe_running:
            self.probe_button.set_sensitive(False)
        self.probe_button.set_label("Остановить проверку" if self.probe_running else "Проверить маршруты")
        self.connect_button.set_label("Отключить" if active else "Подключить")
        self.connect_button.set_sensitive((self.vpn_process is not None and not self.vpn_starting and not self.switching_route) or
                                          (matches and valid and not busy))
        self.load_button.set_sensitive(not busy)
        self.entry.set_sensitive(not busy)
        self.listbox.set_sensitive(not selecting and not self.results_stale)
        self.device_scroll.set_sensitive(not busy and not self.binding_busy)
        self.choose_button.set_sensitive(not selecting and bool(self.current_profiles))
        self.summary_title.set_text("Текущее подключение" if self.connected_since is not None else "Выбранный маршрут")
        for style in ("connected", "checking"):
            self.home_state.remove_css_class(style)
        if self.connected_since is not None:
            self.home_state.add_css_class("connected")
            self.sidebar_state.set_text("VPN подключён")
            self.connection_hint.set_text("Соединение активно")
        elif self.vpn_starting:
            self.home_state.add_css_class("checking")
            self.home_state.set_text("Подключение…")
            self.sidebar_state.set_text("Подключение…")
            self.connection_hint.set_text("Подтвердите системный запрос")
        elif self.probe_running or self.loading_profiles:
            self.home_state.add_css_class("checking")
            self.home_state.set_text("Проверяем маршруты" if self.probe_running else "Загрузка подписки")
            self.sidebar_state.set_text("VPN отключён")
            self.connection_hint.set_text("Подключение станет доступно после проверки")
        else:
            self.home_state.set_text("Готов к подключению" if valid and matches else "Не подключён")
            self.sidebar_state.set_text("VPN отключён")
            self.connection_hint.set_text("Маршрут проверен" if valid and matches else "Сначала проверьте маршруты" if self.current_profiles else "")
        self.update_best_routes()
        self.external_sync_controls()
        has_source = bool(self.current_profiles) or self.loading_profiles or bool(self.active_source)
        self.welcome_panel.set_visible(not has_source)
        self.connection_panel.set_visible(has_source)
        self.add_subscription_button.set_visible(False)
        self.source_name.set_text((self.active_source or {}).get("name") or "")
        self.render_source_choices()
        self.source_switch_button.set_sensitive(not busy)
        self.source_switch_button.set_tooltip_text("Отключите VPN для смены подписки" if active else "Выбрать подписку для подключения")
        if getattr(self, "account_use_button", None):
            source = self.active_source or {}
            self.account_use_button.set_visible(bool(self.session) and
                (source.get("kind") != "quantway" or source.get("id") != self.session.get("current_device")))
            if self.account_use_button.get_visible():
                self.renew_button.remove_css_class("primary")
            else:
                self.renew_button.add_css_class("primary")
        self.sync_tinc_controls()
        tinc_chosen = self.tinc_worker_active or self.connection_mode == "tinc"
        self.routes_lock_hint.set_visible(active or tinc_chosen)
        self.routes_lock_hint.set_text("Это маршруты обычного VPN. Сейчас выбран Tinc; способ подключения меняется на Главной." if tinc_chosen else
                                       "Для смены выберите маршрут и нажмите «Переключить маршрут». Новую проверку можно запустить после отключения VPN.")
        self.home_state.set_visible(self.connected_since is not None or self.vpn_starting or self.switching_route)
        self.connect_button.update_property([Gtk.AccessibleProperty.DESCRIPTION], [self.connection_hint.get_text()])
        self.update_notice()

        # Reflow before GTK allocates a longer status (e.g. checking), so an
        # already narrow window does not grow just to fit the header row.
        self.update_responsive_layout(None, None)

    @staticmethod
    def format_days(value):
        if value is None:
            return ""
        count = max(0, int(value))
        form = "дней" if 11 <= count % 100 <= 14 else "день" if count % 10 == 1 else "дня" if count % 10 in (2, 3, 4) else "дней"
        return f"{count} {form}"

    def refresh_cabinet(self, _button):
        if not self.session or self.binding_busy:
            return
        context = self.account_context()
        rebind = bool(self.session.get("needs_replacement") or self.session.get("device_error") or
                      (not self.session.get("current_device") and self.session.get("identity", {}).get("kind") != "owner"))
        self.binding_busy = rebind
        self.device_scroll.set_sensitive(not rebind)
        self.account_refresh_button.set_sensitive(False)
        self.status.set_text("Обновляем кабинет…")
        threading.Thread(target=self.refresh_account_worker, args=(context, rebind), daemon=True).start()

    def account_context(self, renewal=False):
        if not self.session:
            return None
        context = (self.account_generation, self.session["cookie"])
        return context + (self.renewal_generation,) if renewal else context

    def account_callback_current(self, context):
        if self.closing:
            return False
        if context is None:
            return True
        return bool(self.session and context[:2] == self.account_context() and
                    (len(context) == 2 or context[2] == self.renewal_generation))

    def reset_account_requests(self):
        # Requests already in flight retain their own cookie and generation.
        self.account_generation += 1
        self.renewal_generation += 1
        self.session = None
        self.reset_account_stats()
        self.pending_account_subscription = None
        self.binding_busy = False
        self.cabinet_serial += 1
        self.cabinet_busy = False
        self.account_links.set_sensitive(True)
        self.account_links.set_visible(False)
        self.account_refresh_button.set_visible(False)
        self.cabinet_content.set_sensitive(True)
        self.plans_box.set_sensitive(True)
        self.login_button.set_sensitive(True)
        self.code.set_sensitive(True)
        self.account_refresh_button.set_sensitive(True)
        self.renew_button.set_sensitive(True)
        self.plans_box.set_visible(False)
        self.invoice_check_button = None
        self._plan_choices = None

    def queue_account_subscription(self, link, context=None, device_id=None):
        context = context or self.account_context()
        if not link or not context or not self.account_callback_current(context):
            return False
        if link == self.loaded_link and self.current_profiles and self.entry.get_text().strip() == link and (not self.results_stale or self.probe_running):
            return False
        self.pending_account_subscription = (context[:2], link, device_id or self.session.get("current_device", ""))
        if self.load_pending_account_subscription():
            return True
        self.status.set_text("Подписка этого компьютера загрузится после отключения VPN." if
                             self.vpn_process is not None or self.vpn_starting or self.connected_since is not None else
                             "Подписка этого компьютера загрузится после текущей проверки.")
        return True

    def load_pending_account_subscription(self):
        pending = self.pending_account_subscription
        if not pending:
            return False
        context, link, device_id = pending
        if not self.account_callback_current(context):
            self.pending_account_subscription = None
            return False
        if self.loading_profiles or self.probe_running or self.vpn_process is not None or self.vpn_starting or self.connected_since is not None or getattr(self, "_device_mutating", False):
            return False
        self.pending_account_subscription = None
        self.select_subscription(link, kind="quantway", source_id=device_id, name="Quantway")
        return True

    def select_subscription(self, link, kind, source_id, name):
        if self.loading_profiles or self.probe_running or self.vpn_starting or self.vpn_process is not None or self.switching_route or getattr(self, "_device_mutating", False):
            self.status.set_text("Дождитесь завершения операции или отключите VPN, чтобы сменить провайдера.")
            return False
        if link != self.loaded_link:
            self.current_profiles = []
            self.selected_profile = None
            self.results_stale = True
            self.loaded_link = ""
            self.show_result([], "")
        self.active_source = {"kind": kind, "id": source_id, "name": name, "link": link}
        self.fetch_source = dict(self.active_source)
        self.entry.set_text(link)
        self.nav_buttons[0].set_active(True)
        self.stack.set_visible_child_name("home")
        self.load(None)
        return True

    def clear_subscription_source(self, kind, source_id):
        source = self.active_source or {}
        if source.get("kind") != kind or source.get("id") != source_id:
            return
        self.invalidate_subscription_operations()
        self.active_source = None
        self.fetch_source = None
        self.loaded_link = ""
        self.entry.set_text("")
        self.current_profiles = []
        self.selected_profile = None
        self.results_stale = True
        self.show_result([], "")

    def subscription_callback_current(self, generation):
        return not self.closing and (generation is None or generation == self.subscription_generation)

    def invalidate_subscription_operations(self):
        # A fetch is read-only and may finish in the background. Its callback
        # cannot restore this source. Probes/VPNs retain their busy slot until
        # their worker has reaped the helper, so network cleanup never overlaps
        # with a replacement tunnel.
        self.subscription_generation += 1
        self.loading_profiles = False
        if self.probe_running:
            self.probe_cancelled = True
            if self._probe_cancel is not None:
                self._probe_cancel.set()
            if self.probe_process is not None:
                try:
                    self.probe_process.terminate()
                except OSError:
                    pass
        if self.vpn_starting or self.vpn_process is not None or self.connected_since is not None:
            self.vpn_generation += 1
            if self.vpn_process is not None:
                try:
                    self.vpn_process.stdin.write("stop\n")
                    self.vpn_process.stdin.flush()
                except (OSError, ValueError, AttributeError):
                    pass

    def on_device_connection_selected(self, device, link, context):
        self.queue_account_subscription(link, context, device_id=device["id"])

    def on_account_device_changed(self, action, data, previous):
        source = self.active_source or {}
        matches = source.get("kind") == "quantway" and source.get("id") == previous.get("id")
        if not matches:
            return False
        if action not in ("delete", "block", "reissue"):
            return False
        self.pending_route_switch = None
        self.switching_route = False
        self.clear_subscription_source("quantway", source.get("id"))
        return action == "reissue"

    def show_login_form(self, _button):
        self.forget_saved_account()
        self.reset_account_requests()
        self.account_row.set_visible(True)
        self.login_heading.set_visible(True)
        self.login_hint.set_visible(True)
        self.login_heading.set_text("Войти в Quantway")
        self.account_summary.set_visible(False)
        self.account_description.set_visible(True)
        self.device_scroll.set_visible(False)
        self.code.grab_focus()

    def update_route_card(self):
        from route_ui import update_route_card
        return update_route_card(self)

    def update_best_routes(self):
        from route_ui import update_best_routes
        return update_best_routes(self)

    def choose_best(self, _button, profile):
        from route_ui import choose_best
        return choose_best(self, profile)

    def apply_route_preview(self, _button=None):
        from route_ui import apply_route_preview
        return apply_route_preview(self)

    def filter_routes(self, *_args):
        from route_ui import filter_routes
        return filter_routes(self)

    def show_progress(self, done, total, generation=None):
        if not self.probe_running or not self.subscription_callback_current(generation) or (generation is not None and generation != self._probe_generation):
            return False
        if total > 0:
            self.progress_percent.set_fraction(min(1, max(0, done / total)))
            self.routes_progress_percent.set_fraction(min(1, max(0, done / total)))
            self.progress_percent.set_text(f"{min(100, done * 100 // total)} %")
            self.progress_text.set_text(f"Проверяем маршруты: {done} из {total}")
            self.routes_progress_text.set_text(f"Проверяем маршруты: {done} из {total}")
            self.routes_progress_percent.set_text(f"{min(100, done * 100 // total)} %")
        else:
            self.progress_percent.pulse()
            self.routes_progress_percent.pulse()
            self.progress_text.set_text("Подготавливаем проверку…")
            self.progress_percent.set_text("")
            self.routes_progress_text.set_text("Подготавливаем проверку…")
            self.routes_progress_percent.set_text("")
        return False

    def call_helper(self, action, payload):
        helper = helper_path()
        proc = subprocess.run([helper, "--json", "--action", action], input=json.dumps(payload) + "\n",
                              text=True, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=180, check=False, **helper_process_options())
        data = json.loads(proc.stdout)
        if proc.returncode:
            raise ServiceFailure(data.get("error") or "Не удалось выполнить действие.", data.get("status", 0), data.get("code", ""))
        return data

    def login(self, _button):
        code = self.code.get_text().strip()
        if not code:
            self.status.set_text("Введите код входа.")
            return
        self.forget_saved_account()
        self.reset_account_requests()
        generation = self.account_generation
        self.login_button.set_sensitive(False)
        self.code.set_sensitive(False)
        self.status.set_text("Входим в кабинет…")
        threading.Thread(target=self.login_worker, args=(code, generation), daemon=True).start()

    def login_worker(self, code, generation):
        try:
            session = self.call_helper("login", {"code": code})["session"]
            GLib.idle_add(self.login_received, session, "", generation)
        except (OSError, subprocess.TimeoutExpired, ValueError, KeyError):
            GLib.idle_add(self.login_received, None, "Не удалось войти. Проверьте код и сеть.", generation)

    def show_devices(self, session, error, generation=None, import_subscription=True):
        if self.closing or (generation is not None and generation != self.account_generation):
            return False
        if generation is None:
            self.reset_account_requests()
        self.login_button.set_sensitive(True)
        self.code.set_sensitive(True)
        if error:
            self.status.set_text(error)
            self.code.grab_focus()
            return False
        self.code.set_text("")
        self.session = session
        self.render_account(session)
        if import_subscription and session.get("device_link") and self.queue_account_subscription(session["device_link"]):
            pass
        elif session.get("device_error"):
            self.status.set_text(session["device_error"])
        else:
            self.status.set_text("Кабинет открыт.")
        return False

    def render_account(self, session):
        self.account_row.set_visible(False)
        self.login_hint.set_visible(False)
        self.login_heading.set_visible(False)
        self.account_summary.set_visible(bool(session.get("account")))
        self.account_refresh_button.set_visible(True)
        self.account_description.set_visible(False)
        sub = session.get("account", {}).get("subscription") or {}
        self.account_headline.set_text(sub.get("headline") or "Подписка Quantway")
        days = sub.get("days_left")
        self.account_days.set_text(self.format_days(days))
        self.account_days.set_visible(days is not None)
        self.account_until.set_text(sub.get("until_text") or "")
        self.account_until.set_visible(bool(self.account_until.get_text()))
        state = sub.get("status") or ""
        self.account_state.set_text({"active": "Активна", "paused": "На паузе", "expired": "Истекла", "trial": "Пробный доступ"}.get(state, ""))
        self.account_state.set_visible(bool(self.account_state.get_text()))
        for style in ("success", "warning", "danger"):
            self.account_state.remove_css_class(style)
        self.account_state.add_css_class("success" if state in ("active", "trial") else "warning")
        self.renew_button.set_visible(session.get("identity", {}).get("kind") == "retail")
        self.account_actions.set_visible(self.renew_button.get_visible())
        self.account_stats_panel.set_visible(session.get("identity", {}).get("kind") != "owner")
        self.refresh_account_stats()
        self.build_account_links()
        previous_use = getattr(self, "account_use_button", None)
        if previous_use is not None and previous_use.get_parent() == self.account_actions:
            self.account_actions.remove(previous_use)
        self.account_use_button = None
        self.renew_button.add_css_class("primary")
        source = self.active_source or {}
        if session.get("device_link"):
            use = button("Использовать подписку Quantway", lambda _b: self.on_device_connection_selected(
                {"id": self.session.get("current_device", "")}, self.session["device_link"], self.account_context()), "primary")
            use.set_sensitive(not session.get("device_error") and sub.get("link_works", True))
            use.set_visible(source.get("kind") != "quantway" or source.get("id") != session.get("current_device"))
            self.account_use_button = use
            self.account_actions.prepend(use)
            if use.get_visible():
                self.renew_button.remove_css_class("primary")
        while child := self.devices.get_first_child():
            self.devices.remove(child)
        needs_replacement = bool(session.get("needs_replacement"))
        self.device_scroll.set_visible(needs_replacement and bool(session.get("devices")))
        if needs_replacement:
            explanation = Gtk.Label(label="Нет свободных мест. Выберите устройство, которое больше не будет подключаться через свою ссылку.", xalign=0)
            explanation.set_wrap(True); explanation.add_css_class("muted"); self.devices.append(explanation)
            for device in session.get("devices") or []:
                device_name = device.get("name") or "Устройство"
                replace_button = Gtk.Button(label="Заменить: " + device_name)
                replace_button.get_child().set_ellipsize(3)
                replace_button.set_tooltip_text(device_name)
                replace_button.set_sensitive(not device.get("blocked", False))
                replace_button.connect("clicked", self.confirm_replacement, device.get("id"), device_name)
                self.devices.append(replace_button)

    def load_plans(self, _button):
        if not self.session:
            return
        if getattr(self, "_plans_account_generation", None) == self.account_generation and hasattr(self, "plan_select"):
            plan_index, method_index = self.plan_select.get_selected(), self.method_select.get_selected()
            self._plan_choices = {
                "plan": self.available_plans[plan_index].get("id") if plan_index < len(self.available_plans) else "",
                "method": self.payment_methods[method_index].get("id") if method_index < len(self.payment_methods) else "",
                "devices": self.extra_devices.get_value_as_int() if self.extra_devices is not None else None,
            }
        self.renewal_generation += 1
        context = self.account_context(renewal=True)
        self.renew_button.set_sensitive(False)
        self.plans_box.set_sensitive(False)
        self.plans_box.set_visible(False)
        self.plans_retry.set_visible(False)
        self.stack.set_visible_child_name("renew")
        self.status.set_text("Загружаем тарифы…")
        threading.Thread(target=self.plans_worker, args=(context,), daemon=True).start()

    def plans_worker(self, context):
        try:
            data = self.call_helper("plans", {"cookie": context[1]})["data"]
            GLib.idle_add(self.show_plans, data, "", context)
        except (OSError, subprocess.TimeoutExpired, ValueError, KeyError):
            GLib.idle_add(self.show_plans, {}, "Не удалось загрузить тарифы.", context)

    def show_plans(self, data, error, context=None):
        if not self.account_callback_current(context):
            return False
        self.renew_button.set_sensitive(True)
        if error:
            self.plans_retry.set_visible(True)
            self.status.set_text(error)
            return False
        while child := self.plans_box.get_first_child():
            self.plans_box.remove(child)
        plans = data.get("plans") or []
        methods = data.get("methods") or []
        if not plans or not methods:
            self.plans_retry.set_visible(True)
            self.status.set_text("Тарифы или способы оплаты сейчас недоступны.")
            return False
        self.available_plans, self.payment_methods = plans, methods
        self._plans_account_generation = self.account_generation
        saved = self._plan_choices or {}
        columns = box(14)
        self.plan_columns = columns
        controls = box(14, style="quiet-panel")
        controls.set_hexpand(True)
        controls.append(label("Условия подписки", "section-title"))
        self.plan_select = Gtk.DropDown.new_from_strings([str(p.get("title") or "Тариф") for p in plans])
        self.plan_select.update_property([Gtk.AccessibleProperty.LABEL], ["Тариф"])
        self.plan_select.set_selected(next((i for i, plan in enumerate(plans) if plan.get("id") == saved.get("plan")), 0))
        controls.append(field("_Тариф", self.plan_select))
        summary = box(12, style="panel")
        summary.set_hexpand(True)
        summary.set_valign(Gtk.Align.START)
        summary.append(label("К оплате", "caption"))
        self.plan_price = label("", "invoice-amount", True)
        summary.append(self.plan_price)
        self.plan_terms = label("", "muted", True)
        summary.append(self.plan_terms)
        self.plan_payment_caption = label("", "caption", True)
        summary.append(self.plan_payment_caption)
        choice = data.get("devices")
        self.extra_devices = None
        if isinstance(choice, dict):
            self.extra_devices = Gtk.SpinButton.new_with_range(0, max(0, int(choice.get("max", 0))), 1)
            self.extra_devices.set_value(max(0, min(int(choice.get("max", 0)), int(saved.get("devices") if saved.get("devices") is not None else choice.get("current", 0)))))
            self.extra_devices.set_width_chars(3)
            self.extra_devices.set_halign(Gtk.Align.START)
            self.extra_devices.update_property([Gtk.AccessibleProperty.LABEL], ["Дополнительные устройства"])
            self.extra_devices.connect("value-changed", self.update_plan_price)
            controls.append(field("_Дополнительные устройства", self.extra_devices))
            if choice.get("monthly"):
                controls.append(label(str(choice["monthly"]), "caption", True))
        self.plan_select.connect("notify::selected", self.update_plan_price)
        self.method_select = Gtk.DropDown.new_from_strings([str(m.get("label") or "Способ оплаты") for m in methods])
        self.method_select.update_property([Gtk.AccessibleProperty.LABEL], ["Способ оплаты"])
        self.method_select.set_selected(next((i for i, method in enumerate(methods) if method.get("id") == saved.get("method")), 0))
        self.method_select.connect("notify::selected", self.update_plan_price)
        controls.append(field("_Способ оплаты", self.method_select))
        columns.append(controls)
        columns.append(summary)
        self.plans_box.append(columns)
        self.invoice_button = button("Создать счёт", self.create_invoice, "primary")
        summary.append(self.invoice_button)
        summary.append(label("Оплата откроется у платёжного провайдера. После оплаты вернитесь в приложение.", "caption", True))
        self.invoice_result = box(12, style="quiet-panel")
        self.invoice_result.set_visible(False)
        self.plans_box.append(self.invoice_result)
        self.update_plan_price()
        self.promo_container = controls
        self.append_promo_controls(data)
        self.plans_box.set_visible(True)
        self.plans_box.set_sensitive(True)
        self.plans_retry.set_visible(False)
        if context is None:
            self.stack.set_visible_child_name("renew")
        self._layout_width = None
        self.update_responsive_layout(None, None)
        self.status.set_text("")
        return False

    def update_plan_price(self, *_args):
        index = self.plan_select.get_selected()
        if index >= len(self.available_plans):
            self.plan_price.set_text("")
            return
        plan = self.available_plans[index]
        amount = plan.get("promo_price") or plan.get("price") or ""
        if self.extra_devices is not None:
            for total in plan.get("totals") or []:
                if total.get("devices") == self.extra_devices.get_value_as_int():
                    amount = total.get("promo_price") or total.get("price") or amount
                    break
        self.plan_price.set_text(str(amount) if amount else "—")
        if hasattr(self, "plan_terms"):
            days = self.format_days(plan.get("days"))
            included = f"Устройств в тарифе: {plan['devices']}" if plan.get("devices") is not None else ""
            extra = self.extra_devices.get_value_as_int() if self.extra_devices is not None else 0
            self.plan_terms.set_text("\n".join(filter(None, (str(plan.get("title") or ""), days, included, f"Дополнительных мест: {extra}" if extra else ""))))
            method_index = self.method_select.get_selected()
            self.plan_payment_caption.set_text(str(self.payment_methods[method_index].get("label") or "") if method_index < len(self.payment_methods) else "")

    def create_invoice(self, _button):
        if not self.session:
            return
        context = self.account_context(renewal=True)
        plan_index, method_index = self.plan_select.get_selected(), self.method_select.get_selected()
        if plan_index >= len(self.available_plans) or method_index >= len(self.payment_methods):
            return
        payload = {"cookie": self.session["cookie"], "plan": self.available_plans[plan_index].get("id", ""), "method": self.payment_methods[method_index].get("id", "")}
        if self.extra_devices is not None:
            payload["devices"] = self.extra_devices.get_value_as_int()
        self.invoice_button.set_sensitive(False)
        self.status.set_text("Создаём счёт…")
        threading.Thread(target=self.invoice_worker, args=(payload, context), daemon=True).start()

    def invoice_worker(self, payload, context):
        try:
            data = self.call_helper("invoice", payload)["data"]
            GLib.idle_add(self.show_invoice, data, "", context, "renew")
        except (OSError, subprocess.TimeoutExpired, ValueError, KeyError):
            GLib.idle_add(self.show_invoice, {}, "Не удалось создать счёт.", context, "renew")

    def show_invoice(self, data, error, context=None, expected_page=None):
        if not self.account_callback_current(context):
            return False
        self.invoice_button.set_sensitive(True)
        if getattr(self, "invoice_check_button", None):
            self.invoice_check_button.set_sensitive(True)
        if error:
            self.status.set_text(error)
            return False
        if expected_page and self.stack.get_visible_child_name() != expected_page:
            self.status.set_text("Счёт создан. Он доступен в разделе «Счета и оплаты».")
            return False
        self.show_account_invoice(data)
        self.status.set_text((data.get("invoice") or {}).get("status") or "Счёт создан.")
        return False

    def open_payment(self, url):
        try:
            Gio.AppInfo.launch_default_for_uri(url, None)
        except GLib.Error:
            self.status.set_text("Не удалось открыть браузер. Скопируйте реквизиты счёта или попробуйте ещё раз.")

    def refresh_account_worker(self, context, rebind=False):
        try:
            data = self.call_helper("account-refresh", {"cookie": context[1], "bind_device": rebind})["data"]
            GLib.idle_add(self.account_refreshed, data, context)
        except (OSError, subprocess.TimeoutExpired, ValueError, KeyError):
            GLib.idle_add(self.account_refresh_failed, context, rebind)

    def account_refresh_failed(self, context=None, rebind=False):
        if not self.account_callback_current(context):
            return False
        if rebind:
            self.binding_busy = False
        self.sync_controls()
        self.account_refresh_button.set_sensitive(not self.binding_busy)
        self.status.set_text("Не удалось обновить кабинет. Попробуйте ещё раз или войдите заново.")
        return False

    def account_refreshed(self, data, context=None):
        if not self.account_callback_current(context):
            return False
        binding = data.get("binding")
        if binding is not None:
            self.binding_busy = False
        self.account_refresh_button.set_sensitive(not self.binding_busy)
        self.session["account"] = data.get("account") or {}
        if "identity" in data:
            self.session["identity"] = data["identity"]
        if binding is not None:
            for key, default in (("devices", []), ("current_device", ""), ("device_link", ""), ("device_error", ""), ("needs_replacement", False)):
                self.session[key] = binding.get(key, default)
        self.render_account(self.session)
        self.sync_controls()
        if binding and binding.get("device_link") and self.queue_account_subscription(binding["device_link"], context):
            return False
        self.status.set_text(self.session.get("device_error") or "Кабинет обновлён.")
        return False

    def check_invoice(self, button, invoice_id):
        context = self.account_context(renewal=True)
        if not context:
            return
        button.set_sensitive(False)
        self.status.set_text("Проверяем оплату…")
        def worker():
            try:
                data = self.call_helper("invoice-status", {"cookie": context[1], "device_id": invoice_id})["data"]
                GLib.idle_add(self.show_invoice, data, "", context)
            except (OSError, subprocess.TimeoutExpired, ValueError, KeyError):
                GLib.idle_add(self.show_invoice, {}, "Не удалось проверить оплату.", context)
        threading.Thread(target=worker, daemon=True).start()

    def confirm_replacement(self, _button, device_id, name):
        if not self.session or not self.session.get("needs_replacement"):
            return
        dialog = Gtk.MessageDialog(transient_for=self.window, modal=True, message_type=Gtk.MessageType.QUESTION,
                                   buttons=Gtk.ButtonsType.NONE, text="Заменить устройство?")
        dialog.set_property("secondary-text", f"Устройство «{name}» потеряет доступ по своей текущей ссылке. Его место займёт этот компьютер.")
        dialog.add_button("Отмена", Gtk.ResponseType.CANCEL)
        dialog.add_button("Заменить", Gtk.ResponseType.ACCEPT)
        dialog.connect("response", self.replacement_response, device_id, self.account_context())
        dialog.present()

    def replacement_response(self, dialog, response, device_id, context=None):
        dialog.destroy()
        if response != Gtk.ResponseType.ACCEPT or not self.account_callback_current(context) or not self.session or not self.session.get("needs_replacement") or self.binding_busy:
            return
        context = context or self.account_context()
        self.binding_busy = True
        self.device_scroll.set_sensitive(False)
        self.account_refresh_button.set_sensitive(False)
        self.status.set_text("Заменяем устройство…")
        threading.Thread(target=self.replacement_worker, args=(device_id, context), daemon=True).start()

    def replacement_worker(self, device_id, context):
        try:
            data = self.call_helper("replace-device", {"cookie": context[1], "device_id": device_id})["data"]
            GLib.idle_add(self.replacement_done, data, "", context)
        except (OSError, subprocess.TimeoutExpired, ValueError, KeyError):
            GLib.idle_add(self.replacement_done, {}, "Не удалось завершить замену. Обновите кабинет и проверьте список устройств.", context)

    def replacement_done(self, data, error, context=None):
        if not self.account_callback_current(context):
            return False
        self.binding_busy = False
        self.account_refresh_button.set_sensitive(True)
        self.device_scroll.set_sensitive(True)
        self.sync_controls()
        if error:
            self.status.set_text(error)
            return False
        self.session["current_device"] = data["device_id"]
        self.session["device_link"] = data["link"]
        self.session["needs_replacement"] = False
        self.session["device_error"] = ""
        self.refresh_account_stats()
        self.device_scroll.set_visible(False)
        self.sync_controls()
        self.queue_account_subscription(data["link"], context)
        return False

    def use_device(self, button, device_id):
        context = self.account_context()
        if not context:
            return
        button.set_sensitive(False)
        self.status.set_text("Получаем подписку…")
        threading.Thread(target=self.device_worker, args=(button, device_id, context), daemon=True).start()

    def device_worker(self, button, device_id, context):
        try:
            data = self.call_helper("device", {"cookie": context[1], "device_id": device_id})
            GLib.idle_add(self.start_device_load, button, data["link"], "", context)
        except (OSError, subprocess.TimeoutExpired, ValueError, KeyError):
            GLib.idle_add(self.start_device_load, button, "", "Не удалось получить подписку устройства.", context)

    def start_device_load(self, button, link, error, context=None):
        if not self.account_callback_current(context):
            return False
        button.set_sensitive(True)
        if error:
            self.status.set_text(error)
        else:
            self.queue_account_subscription(link, context)
        return False

    def subscription_changed(self, _entry):
        if hasattr(self, "listbox"):
            self.sync_controls()

    def load(self, _button):
        if self.loading_profiles or self.probe_running or self.vpn_starting or self.vpn_process is not None or getattr(self, "_device_mutating", False):
            return
        link = self.entry.get_text().strip()
        if not link:
            self.status.set_text("Введите полную ссылку на подписку.")
            self.entry.grab_focus()
            return
        self.loading_profiles = True
        self.subscription_generation += 1
        generation = self.subscription_generation
        self.connection_title.set_text("Загружаем подписку")
        self.connection_description.set_text("После загрузки проверим доступные маршруты.")
        self.sync_controls()
        self.status.set_text("Загружаем подписку…")
        threading.Thread(target=self.fetch, args=(link, generation), daemon=True).start()

    def fetch(self, link, generation):
        if self.closing or generation != self.subscription_generation:
            return
        helper = helper_path()
        try:
            proc = subprocess.run([helper, "--json"], input=link + "\n", text=True,
                                  stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=30, check=False, **helper_process_options())
            data = json.loads(proc.stdout)
            error = data.get("error", "") if proc.returncode else ""
            profiles = data.get("profiles") or []
        except (OSError, subprocess.TimeoutExpired, ValueError):
            error, profiles = "Не удалось запустить загрузку маршрутов.", []
        GLib.idle_add(self.show_result, profiles, error, link, generation)

    def start_probe(self, _button):
        if self.tinc_worker_active:
            return
        if self.probe_running:
            self.probe_cancelled = True
            if self._probe_cancel is not None:
                self._probe_cancel.set()
            self.probe_button.set_sensitive(False)
            self.status.set_text("Останавливаем проверку…")
            if self.probe_process is not None:
                try:
                    self.probe_process.terminate()
                except ProcessLookupError:
                    pass
            return
        if self.vpn_process is not None or self.vpn_starting or self.loading_profiles or not self.loaded_link or getattr(self, "_device_mutating", False):
            return
        self.probe_running = True
        self.subscription_generation += 1
        generation = self.subscription_generation
        self._probe_generation = generation
        self._probe_cancel = threading.Event()
        self.connection_title.set_text("Ищем лучший маршрут")
        self.connection_description.set_text("Проверяем доступность в вашей текущей сети.")
        self.results_stale = True
        self.update_route_card()
        self.sync_controls()
        self.progress_box.set_visible(True)
        self.routes_progress_box.set_visible(True)
        self.routes_spinner.start()
        self.routes_progress_text.set_text("Подготавливаем проверку…")
        self.routes_progress_percent.set_text("")
        self.routes_progress_percent.set_fraction(0)
        self.update_best_routes()
        self.progress_spinner.start()
        self.progress_text.set_text("Подготавливаем проверку…")
        self.progress_percent.set_text("")
        self.progress_percent.set_fraction(0)
        self.connect_button.set_sensitive(False)
        self.probe_cancelled = False
        self.probe_button.set_label("Остановить проверку")
        self.load_button.set_sensitive(False)
        self.status.set_text("")
        threading.Thread(target=self.probe_worker, args=(self.loaded_link, generation, self._probe_cancel), daemon=True).start()

    def probe_worker(self, link, generation, cancel):
        helper = helper_path()
        process = None
        timer = None
        data = {}
        error = ""
        try:
            if self.closing or generation != self.subscription_generation or cancel.is_set():
                return
            process = subprocess.Popen([helper, "--json", "--action", "probe", "--progress"], stdin=subprocess.PIPE,
                                       stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True, **helper_process_options())
            self.probe_process = process
            if self.closing or generation != self.subscription_generation or cancel.is_set():
                return
            process.stdin.write(json.dumps({"link": link}) + "\n")
            process.stdin.close()
            timer = threading.Timer(600, process.kill)
            timer.daemon = True
            timer.start()
            for line in process.stdout:
                message = json.loads(line)
                if message.get("type") == "progress":
                    GLib.idle_add(self.show_progress, int(message.get("done", 0)), int(message.get("total", 0)), generation)
                else:
                    data = message
            process.wait()
            error = (data.get("error") or "Не удалось проверить маршруты.") if process.returncode else ""
        except (OSError, subprocess.TimeoutExpired, ValueError, BrokenPipeError):
            error = "Не удалось проверить маршруты. Проверьте сеть и попробуйте позже."
        finally:
            if timer is not None:
                timer.cancel()
            if process is not None:
                self.reap_helper(process)
            if self.probe_process is process:
                self.probe_process = None
            if cancel.is_set():
                error = "Проверка остановлена."
            GLib.idle_add(self.show_probe, data.get("results") or [], error, generation)

    @staticmethod
    def reap_helper(process):
        # Completion is delivered only after the helper has finished cleanup.
        # A malformed output or cancellation must not leave a running prober.
        if process.poll() is None:
            try:
                process.terminate()
            except OSError:
                pass
        try:
            process.wait(timeout=15)
        except subprocess.TimeoutExpired:
            try:
                process.kill()
            except OSError:
                pass
            process.wait()

    def show_probe(self, results, error, generation=None):
        if self.closing or (generation is not None and generation != self._probe_generation):
            return False
        self.probe_running = False
        self._probe_generation = None
        self._probe_cancel = None
        self.progress_spinner.stop()
        self.progress_box.set_visible(False)
        self.routes_spinner.stop()
        self.routes_progress_box.set_visible(False)
        self.probe_button.set_label("Проверить")
        self.load_button.set_sensitive(True)
        if not self.subscription_callback_current(generation):
            self.sync_controls()
            self.load_pending_account_subscription()
            return False
        if self.load_pending_account_subscription():
            return False
        self.probe_button.set_sensitive(bool(self.current_profiles) and self.entry.get_text().strip() == self.loaded_link)
        if error:
            self.results_stale = True
            self.connection_title.set_text("Проверка не завершена")
            self.connection_description.set_text("Повторите проверку, чтобы выбрать актуальный маршрут.")
            self.update_route_card()
            self.sync_controls()
            self.status.set_text(error + " Повторите проверку перед подключением.")
            return False
        self.results_stale = False
        self.last_checked = time.monotonic()
        by_id = {result.get("job_id"): result for result in results}
        profiles = []
        for profile in self.current_profiles:
            profile = dict(profile)
            result = by_id.get(profile.get("job_id")) or {}
            profile["outcome"] = result.get("outcome")
            profile["job"] = result.get("job")
            profile["control_url"] = result.get("control_url")
            profile["elapsed_ms"] = result.get("elapsed_ms", 0)
            profiles.append(profile)
        profiles.sort(key=lambda profile: (profile.get("outcome") != "ok", profile.get("elapsed_ms", 0)))
        self.show_result(profiles, "", generation=generation)
        self.selection_automatic = True
        self.selected_profile = next((profile for profile in profiles if profile.get("outcome") == "ok" and profile.get("job")), None)
        self.connect_button.set_sensitive(bool(self.selected_profile and self.selected_profile.get("job")))
        for row in self.listbox_rows():
            if getattr(row, "profile", {}).get("job_id") == (self.selected_profile or {}).get("job_id"):
                self.selecting_automatically = True
                self.listbox.select_row(row)
                self.selecting_automatically = False
                break
        self.update_route_card()
        self.sync_controls()
        working = sum(result.get("outcome") == "ok" for result in results)
        if self.selected_profile:
            self.connection_title.set_text("Готово к подключению")
            self.connection_description.set_text("Лучший маршрут выбран для вашей текущей сети.")
            self.status.set_text(f"Проверка завершена. Работает {working} из {len(results)} маршрутов.")
        else:
            self.connection_title.set_text("Рабочий маршрут не найден")
            self.connection_description.set_text("Попробуйте другую сеть или повторите проверку.")
            self.status.set_text(f"Рабочих маршрутов: {working} из {len(results)}")
        return False

    def listbox_rows(self):
        rows = []
        row = self.listbox.get_first_child()
        while row:
            rows.append(row)
            row = row.get_next_sibling()
        return rows

    def select_route(self, _box, row):
        from route_ui import select_route
        return select_route(self, row)

    def restore_selected_row(self):
        from route_ui import restore_selected_row
        return restore_selected_row(self)

    def confirm_route_switch(self, profile):
        if profile.get("job_id") == (self.selected_profile or {}).get("job_id") or self.route_switch_dialog or self.switching_route:
            return
        dialog = Gtk.MessageDialog(transient_for=self.window, modal=True, buttons=Gtk.ButtonsType.NONE,
                                   message_type=Gtk.MessageType.QUESTION, text="Переключить маршрут?")
        dialog.set_property("secondary-text", "Подключиться через «" + (profile.get("label") or "выбранный маршрут") + "»? Соединение будет ненадолго прервано.")
        dialog.add_button("Отмена", Gtk.ResponseType.CANCEL)
        dialog.add_button("Переключить", Gtk.ResponseType.ACCEPT)
        self.route_switch_dialog = dialog
        dialog.connect("response", self.route_switch_response, profile, self.loaded_link)
        dialog.present()

    def route_switch_response(self, dialog, response, profile, link):
        dialog.destroy()
        self.route_switch_dialog = None
        if response != Gtk.ResponseType.ACCEPT or self.closing or link != self.loaded_link or self.results_stale or self.probe_running or self.loading_profiles:
            return
        current = next((p for p in self.current_profiles if p.get("job_id") == profile.get("job_id") and p.get("outcome") == "ok" and p.get("job")), None)
        if current is None:
            return
        if self.vpn_process is None:
            self.connected_since = None
            self.choose_best(None, current)
            self.toggle_vpn(None)
            return
        self.pending_route_switch = dict(current)
        self.switching_route = True
        self.sync_controls()
        self.status.set_text("Переключаем маршрут…")
        try:
            self.vpn_process.stdin.write("stop\n")
            self.vpn_process.stdin.flush()
        except (OSError, ValueError, AttributeError):
            self.pending_route_switch = None
            self.switching_route = False
            self.sync_controls()
            self.status.set_text("Не удалось остановить текущее соединение. Отключите VPN и попробуйте снова.")

    def close_window(self, _window):
        self.closing = True
        if self.tinc_change_dialog:
            self.tinc_change_dialog.destroy()
            self.tinc_change_dialog = None
        self.close_auth_persistence()
        if hasattr(self, "stop_support_poll"):
            self.stop_support_poll()
        self.subscription_generation += 1
        if self._probe_cancel is not None:
            self._probe_cancel.set()
        self.vpn_generation += 1
        self.pending_route_switch = None
        if self.connection_timer is not None:
            GLib.source_remove(self.connection_timer)
            self.connection_timer = None
        if self.tinc_mesh_timer is not None:
            GLib.source_remove(self.tinc_mesh_timer)
            self.tinc_mesh_timer = None
        if self.probe_process:
            self.probe_process.terminate()
        if self.vpn_process and self.vpn_process.stdin:
            try:
                self.vpn_process.stdin.write("stop\n")
                self.vpn_process.stdin.flush()
                self.vpn_process.stdin.close()
            except (OSError, ValueError):
                pass
        return False

    def toggle_vpn(self, _button):
        if self.closing:
            return
        if self.vpn_process is None and (self._vpn_worker_generation is not None or self.vpn_starting):
            return
        # The running helper owns Stop, even if its account or selected mode
        # changed. Once it exits, Connect follows the explicit selected mode.
        if self.tinc_worker_active:
            self.toggle_tinc_vpn()
            return
        if self.vpn_process is not None:
            self.connect_button.set_sensitive(False)
            self.status.set_text("Отключаем VPN…")
            try:
                self.vpn_process.stdin.write("stop\n")
                self.vpn_process.stdin.flush()
            except (OSError, ValueError, AttributeError):
                pass
            return
        if self._vpn_worker_generation is not None or self.vpn_starting or self.switching_route:
            return
        if self.connection_mode == "tinc":
            self.toggle_tinc_vpn()
            return
        if self.vpn_starting or self.probe_running or self.loading_profiles or self.results_stale or not self.selected_profile or getattr(self, "_device_mutating", False):
            return
        self.vpn_starting = True
        self.update_best_routes()
        self.connect_button.set_sensitive(False)
        self.probe_button.set_sensitive(False)
        self.load_button.set_sensitive(False)
        self.listbox.set_sensitive(False)
        self.device_scroll.set_sensitive(False)
        self.home_state.set_text("Подключение…")
        self.connection_title.set_text("Устанавливаем соединение")
        self.connection_description.set_text("Подтвердите запрос администратора, чтобы изменить настройки сети.")
        self.status.set_text("Подтвердите системный запрос для подключения VPN.")
        self.sync_controls()
        profile = dict(self.selected_profile)
        self.vpn_generation += 1
        self._vpn_worker_generation = self.vpn_generation
        threading.Thread(target=self.vpn_worker, args=(profile, self.vpn_generation), daemon=True).start()

    def vpn_worker(self, profile, generation):
        helper = helper_path()
        process = None
        error = ""
        connected = False
        try:
            if self.closing or generation != self.vpn_generation:
                return
            process = subprocess.Popen([helper, "--json", "--action", "connect"], stdin=subprocess.PIPE,
                                       stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True, **helper_process_options())
            self.vpn_process = process
            if self.closing or generation != self.vpn_generation:
                return
            process.stdin.write(json.dumps({"job": profile["job"], "control_url": profile["control_url"]}) + "\n")
            process.stdin.flush()
            response = json.loads(process.stdout.readline())
            if response.get("status") != "connected":
                error = response.get("error") or "Не удалось подключиться."
            else:
                connected = True
                GLib.idle_add(self.vpn_connected, generation)
            # The helper can report a cleanup failure after the initial
            # connection. Consume the complete protocol before a pending
            # mode switch is allowed to launch another privileged session.
            for line in process.stdout:
                event = json.loads(line)
                if not isinstance(event, dict):
                    raise ValueError("Invalid helper status")
                if event.get("status") == "failed" or event.get("error"):
                    error = "Не удалось полностью восстановить сетевые настройки. Новый режим не будет запущен автоматически."
            process.wait()
            if process.returncode and not error:
                error = "Предыдущее подключение завершилось с ошибкой. Новый режим не будет запущен автоматически."
        except (OSError, ValueError, KeyError, BrokenPipeError):
            error = ("Не удалось корректно завершить подключение. Новый режим не будет запущен автоматически."
                     if connected else "Не удалось запустить подключение.")
        finally:
            if process is not None:
                self.reap_helper(process)
            if self.vpn_process is process:
                self.vpn_process = None
            GLib.idle_add(self.vpn_worker_finished, error, connected, generation)

    def vpn_worker_finished(self, error, connected, generation):
        if generation != self._vpn_worker_generation:
            return False
        self._vpn_worker_generation = None
        if self.closing:
            return False
        if generation != self.vpn_generation:
            # Invalidation cancelled this source, but its worker still owned
            # the cleanup slot. Release it only now, after the helper exited.
            self.pending_route_switch = None
            self.switching_route = False
            return self.vpn_stopped(error or "Подключение отменено.", False, self.vpn_generation)
        return self.vpn_stopped(error, connected, generation)

    def vpn_connected(self, generation=None):
        if self.closing or (generation is not None and generation != self.vpn_generation):
            return False
        self.vpn_starting = False
        self.connect_button.set_label("Отключить")
        self.connect_button.set_sensitive(True)
        self.connected_since = time.monotonic()
        if self.connection_timer is None:
            self.connection_timer = GLib.timeout_add_seconds(1, self.update_connection_time)
        self.update_connection_time()
        self.update_best_routes()
        self.connection_title.set_text("VPN подключён")
        self.connection_description.set_text("Соединение установлено. Ваш трафик проходит через выбранный маршрут.")
        self.status.set_text("")
        self.sync_controls()
        return False

    def update_connection_time(self):
        if self.connected_since is None:
            self.connection_timer = None
            return False
        # A country change can show reconnecting before the helper replies.
        # Retain the clock for a refused change without painting over that state.
        if self.tinc_worker_active and self.tinc_status.get("status") != "connected":
            return True
        elapsed = int(time.monotonic() - self.connected_since)
        self.home_state.set_text(f"Подключён  {elapsed // 3600:02d}:{elapsed // 60 % 60:02d}:{elapsed % 60:02d}")
        return True

    def vpn_stopped(self, error, connected, generation=None):
        if self.closing or (generation is not None and generation != self.vpn_generation):
            return False
        self.vpn_starting = False
        self.connected_since = None
        # Every helper shares this terminal transition. Tinc and pending mode
        # transitions may return early, so reset the common action first.
        self.connect_button.set_label("Подключить")
        if self.finish_tinc_transition(error, connected):
            return False
        switch = self.pending_route_switch
        self.pending_route_switch = None
        self.switching_route = False
        if switch and not error and not self.closing and not self.results_stale:
            self.selection_automatic = False
            self.selected_profile = switch
            self.restore_selected_row()
            self.mark_selected_route()
            self.update_route_card()
            self.connect_button.set_label("Подключить")
            self.toggle_vpn(None)
            return False
        self.update_best_routes()
        self.connect_button.set_label("Подключить")
        self.home_state.set_text("Не подключён")
        self.connection_title.set_text("VPN отключён")
        self.connection_description.set_text("Выберите рабочий маршрут и нажмите «Подключить».")
        self.connect_button.set_sensitive(bool(self.selected_profile and self.selected_profile.get("job")))
        self.load_button.set_sensitive(True)
        self.listbox.set_sensitive(True)
        self.device_scroll.set_sensitive(True)
        self.probe_button.set_sensitive(bool(self.current_profiles) and self.entry.get_text().strip() == self.loaded_link)
        self.status.set_text(error or ("VPN отключён." if connected else "Не удалось подключиться."))
        self.sync_controls()
        self.load_pending_account_subscription()
        return False

    def mark_selected_route(self):
        from route_ui import mark_selected_route
        return mark_selected_route(self)

    def show_result(self, profiles, error, loaded_link=None, generation=None):
        from route_ui import show_result
        return show_result(self, profiles, error, loaded_link, generation)


if __name__ == "__main__":
    raise SystemExit(Quantway().run(None))
