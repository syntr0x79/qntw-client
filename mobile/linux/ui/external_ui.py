"""Third-party subscription sources, separate from the Quantway account."""
from urllib.parse import urlsplit
from uuid import uuid4

import gi
gi.require_version("Gtk", "4.0")
from gi.repository import Gtk, Pango

from desktop_ui import box, button, disclosure, field, label, scrolled
from desktop_ui import heading as title_label


class ExternalMixin:
    def init_external_state(self):
        self.external_subscriptions = []
        self._external_rows = []

    def build_external_page(self):
        page = box(16)
        page.append(title_label("Внешние подписки"))
        page.append(label("Выберите провайдера для обычного VPN. После проверки подключение включается на Главной.", "muted", True))
        self.external_list = box(10)
        self.external_list.add_css_class("source-list")
        page.append(self.external_list)
        self.external_empty = label("Провайдеров пока нет. Добавьте подписку по ссылке ниже.", "muted", True)
        page.append(self.external_empty)
        form = box(12, style="panel")
        self.external_name = Gtk.Entry()
        self.external_name.set_max_length(80)
        self.external_name.set_placeholder_text("Например, рабочий VPN")
        form.append(field("_Название провайдера", self.external_name))
        self.external_url = Gtk.Entry()
        self.external_url.set_visibility(False)
        self.external_url.set_placeholder_text("https://…")
        self.external_url.connect("activate", self.add_external_subscription)
        form.append(field("_Ссылка на подписку", self.external_url))
        self.load_button = button("Добавить и проверить", self.add_external_subscription, "primary")
        form.append(self.load_button)
        form.append(label("Получите полную ссылку у своего VPN-провайдера. Название можно выбрать любое.", "caption", True))
        self.external_expander = disclosure("Добавить провайдера", form, expanded=not self.external_subscriptions)
        page.append(self.external_expander)
        page.append(label("Внешние подписки хранятся в памяти до закрытия приложения.", "caption", True))

        # Existing transport operations use this private active link. It must
        # never share a visible field with a third-party provider's URL.
        self.entry = Gtk.Entry()
        self.entry.connect("changed", self.subscription_changed)
        self.render_external_subscriptions()
        return scrolled(page)

    def external_busy(self):
        return bool(self.loading_profiles or self.probe_running or self.vpn_starting or getattr(self, "switching_route", False) or getattr(self, "_device_mutating", False) or
                    self.vpn_process is not None or self.connected_since is not None or
                    getattr(self, "_vpn_worker_generation", None) is not None or getattr(self, "tinc_worker_active", False))

    @staticmethod
    def validate_external_link(value):
        link = value.strip()
        try:
            parsed = urlsplit(link)
            valid = (parsed.scheme.lower() == "https" and parsed.hostname and
                     parsed.username is None and parsed.password is None and not parsed.fragment and
                     not any(character.isspace() for character in link) and len(link) <= 8192)
            # Accessing port validates malformed authority values as well.
            parsed.port
        except ValueError:
            valid = False
        return link if valid else ""

    def add_external_subscription(self, _button):
        if self.external_busy():
            return
        link = self.validate_external_link(self.external_url.get_text())
        if not link:
            self.status.set_text("Введите полную HTTPS-ссылку на внешнюю подписку.")
            self.external_url.grab_focus()
            return
        name = " ".join(self.external_name.get_text().split()) or urlsplit(link).hostname
        source = next((item for item in self.external_subscriptions if item["link"] == link), None)
        if source is None:
            source = {"id": uuid4().hex, "name": name[:80], "link": link}
            self.external_subscriptions.append(source)
        self.external_name.set_text("")
        self.external_url.set_text("")
        self.render_external_subscriptions()
        self.external_expander.set_expanded(False)
        self.use_external_subscription(None, source["id"])

    def use_external_subscription(self, _button, source_id):
        if self.external_busy():
            return
        source = next((item for item in self.external_subscriptions if item["id"] == source_id), None)
        if source:
            self.select_subscription(source["link"], kind="external", source_id=source["id"], name=source["name"])

    def render_external_subscriptions(self):
        if not hasattr(self, "external_list"):
            return
        while child := self.external_list.get_first_child():
            self.external_list.remove(child)
        self._external_rows = []
        for source in self.external_subscriptions:
            panel = box(12, style="quiet-panel")
            panel.add_css_class("source-row")
            heading = box(10, True)
            words = box(4)
            words.set_hexpand(True)
            title = label(source["name"], "section-title")
            title.set_ellipsize(Pango.EllipsizeMode.END)
            title.set_tooltip_text(source["name"])
            words.append(title)
            host = urlsplit(source["link"]).hostname or "VPN-провайдер"
            host_label = label(host, "caption")
            host_label.set_ellipsize(Pango.EllipsizeMode.END)
            host_label.set_tooltip_text(host)
            words.append(host_label)
            selected = label("Выбрана для подключения", "source-selected")
            words.append(selected)
            heading.append(words)
            use = button("Выбрать и проверить", lambda b, sid=source["id"]: self.use_external_subscription(b, sid), "primary")
            use.add_css_class("source-choice")
            menu = Gtk.MenuButton(icon_name="view-more-symbolic")
            menu.set_valign(Gtk.Align.CENTER)
            menu.set_tooltip_text("Действия с подпиской «" + source["name"] + "»")
            menu.update_property([Gtk.AccessibleProperty.LABEL], ["Действия с подпиской «" + source["name"] + "»"])
            popover = Gtk.Popover()
            menu_box = box(4)
            menu_box.add_css_class("workspace")
            for caption, callback in (
                    ("Переименовать", lambda _b, sid=source["id"], pop=popover: self.rename_external_subscription(pop, sid)),
                    ("Изменить ссылку", lambda _b, sid=source["id"], pop=popover: self.edit_external_link(pop, sid)),
                    ("Удалить из приложения", lambda _b, sid=source["id"], pop=popover: self.remove_external_subscription(pop, sid))):
                item = button(caption, callback, "flat")
                item.set_halign(Gtk.Align.FILL)
                if caption == "Удалить из приложения":
                    item.add_css_class("danger-action")
                menu_box.append(item)
            popover.set_child(menu_box)
            menu.set_popover(popover)
            heading.append(menu)
            panel.append(heading)
            panel.append(use)
            self.external_list.append(panel)
            self._external_rows.append({"id": source["id"], "use": use, "menu": menu, "selected": selected})
        self.external_empty.set_visible(not self.external_subscriptions)
        if not self.external_subscriptions:
            self.external_expander.set_expanded(True)
        self.external_sync_controls()

    def external_sync_controls(self):
        if not hasattr(self, "external_url"):
            return
        busy = self.external_busy()
        for widget in (self.external_name, self.external_url, self.load_button):
            widget.set_sensitive(not busy)
        active = getattr(self, "active_source", None) or {}
        for row in self._external_rows:
            selected = active.get("kind") == "external" and active.get("id") == row["id"]
            row["selected"].set_visible(selected)
            row["use"].set_label("Обновить маршруты" if selected else "Выбрать и проверить")
            if selected:
                row["use"].remove_css_class("primary")
            else:
                row["use"].add_css_class("primary")
            row["use"].set_tooltip_text("Обновить маршруты этой подписки" if selected else "Использовать эту подписку для подключения")
            row["use"].set_sensitive(not busy)
            row["menu"].set_sensitive(not busy)

    @staticmethod
    def _external_dialog_actions(dialog, accept_caption, danger=False):
        dialog.add_css_class("quantway")
        dialog.get_content_area().add_css_class("workspace")
        dialog.get_content_area().set_spacing(12)
        cancel = dialog.add_button("Отмена", Gtk.ResponseType.CANCEL)
        accept = dialog.add_button(accept_caption, Gtk.ResponseType.ACCEPT)
        accept.add_css_class("danger-action" if danger else "primary")
        area = accept.get_parent()
        area.add_css_class("workspace")
        area.set_margin_start(18)
        area.set_margin_end(18)
        area.set_margin_bottom(16)
        if isinstance(area, Gtk.Box):
            area.set_spacing(8)
        dialog.set_default_response(Gtk.ResponseType.CANCEL if danger else Gtk.ResponseType.ACCEPT)
        return cancel

    def rename_external_subscription(self, popover, source_id):
        popover.popdown()
        if self.external_busy():
            return
        source = next((item for item in self.external_subscriptions if item["id"] == source_id), None)
        if not source:
            return
        dialog = Gtk.Dialog(title="Название провайдера", transient_for=self.window, modal=True, destroy_with_parent=True)
        self._external_dialog_actions(dialog, "Сохранить")
        content = dialog.get_content_area()
        content.set_margin_top(18)
        content.set_margin_bottom(18)
        content.set_margin_start(18)
        content.set_margin_end(18)
        entry = Gtk.Entry(text=source["name"], max_length=80, activates_default=True)
        content.append(field("_Название", entry))
        error = label("Введите название провайдера.", wrap=True)
        error.add_css_class("inline-error")
        error.set_visible(False)
        content.append(error)

        def respond(window, response):
            if response == Gtk.ResponseType.ACCEPT:
                name = " ".join(entry.get_text().split())
                if not name:
                    error.set_visible(True)
                    entry.grab_focus()
                    return
                source["name"] = name
                for attribute in ("active_source", "loaded_source"):
                    active = getattr(self, attribute, None) or {}
                    if active.get("kind") == "external" and active.get("id") == source_id:
                        active["name"] = name
                self.render_external_subscriptions()
                self.sync_controls()
            window.destroy()

        dialog.connect("response", respond)
        dialog.present()
        entry.grab_focus()

    def edit_external_link(self, popover, source_id):
        popover.popdown()
        if self.external_busy():
            return
        source = next((item for item in self.external_subscriptions if item["id"] == source_id), None)
        if not source:
            return
        dialog = Gtk.Dialog(title="Ссылка внешней подписки", transient_for=self.window, modal=True, destroy_with_parent=True)
        self._external_dialog_actions(dialog, "Сохранить")
        content = dialog.get_content_area()
        for side in ("top", "bottom", "start", "end"):
            getattr(content, "set_margin_" + side)(18)
        entry = Gtk.Entry(text=source["link"], visibility=False, activates_default=True)
        entry.set_width_chars(35)
        content.append(field("_Ссылка на подписку", entry))
        error = label("", wrap=True)
        error.add_css_class("inline-error")
        error.set_visible(False)
        content.append(error)

        def respond(window, response):
            if response != Gtk.ResponseType.ACCEPT:
                window.destroy()
                return
            link = self.validate_external_link(entry.get_text())
            message = "Введите полную HTTPS-ссылку на подписку." if not link else "Дождитесь завершения текущей операции." if self.external_busy() else ""
            if message:
                error.set_text(message)
                error.set_visible(True)
                entry.grab_focus()
                return
            previous = source["link"]
            if link != previous:
                active = getattr(self, "active_source", None) or {}
                if active.get("kind") == "external" and active.get("id") == source_id:
                    if self.select_subscription(link, kind="external", source_id=source_id, name=source["name"]) is False:
                        return
                source["link"] = link
                self.render_external_subscriptions()
            window.destroy()

        dialog.connect("response", respond)
        dialog.present()
        entry.grab_focus()

    def remove_external_subscription(self, popover, source_id):
        popover.popdown()
        if self.external_busy():
            return
        source = next((item for item in self.external_subscriptions if item["id"] == source_id), None)
        if not source:
            return
        dialog = Gtk.MessageDialog(transient_for=self.window, modal=True, destroy_with_parent=True,
                                   message_type=Gtk.MessageType.QUESTION, buttons=Gtk.ButtonsType.NONE,
                                   text="Удалить внешнюю подписку?")
        dialog.set_property("secondary-text", "«" + source["name"] + "» будет удалена из приложения. Для повторного добавления понадобится её ссылка.")
        cancel = self._external_dialog_actions(dialog, "Удалить", danger=True)

        def respond(window, response):
            window.destroy()
            if response != Gtk.ResponseType.ACCEPT or self.external_busy():
                return
            self.external_subscriptions = [item for item in self.external_subscriptions if item["id"] != source_id]
            self.clear_subscription_source("external", source_id)
            self.render_external_subscriptions()
            self.status.set_text("Внешняя подписка удалена из приложения.")

        dialog.connect("response", respond)
        dialog.present()
        cancel.grab_focus()
