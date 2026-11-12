"""Native account sections backed by the same API as the web cabinet."""
import base64
import subprocess
import threading
from urllib.parse import quote, urlparse

from gi.repository import Gdk, GdkPixbuf, Gio, GLib, Gtk, Pango

from desktop_ui import action_row, box, button, disclosure, field, heading, label, scrolled
from support_ui import SupportChatMixin


class CabinetMixin(SupportChatMixin):
    def init_cabinet_state(self):
        self.cabinet_serial = 0
        self.cabinet_busy = False
        self._cabinet_loading = False
        self._cabinet_refresh = None
        self._cabinet_back = None
        self.init_support_chat_state()

    def build_cabinet_pages(self):
        page = box(18, style="cabinet-page")
        self.cabinet_header = box(12, True)
        self.cabinet_header.add_css_class("section-header")
        self.cabinet_back_button = self.support_icon_button("go-previous-symbolic", "Назад в кабинет", lambda _b: self._cabinet_back() if self._cabinet_back else self.stack.set_visible_child_name("account"))
        self.cabinet_header.append(self.cabinet_back_button)
        words = box(4)
        words.set_hexpand(True)
        self.cabinet_breadcrumb = label("Кабинет", "section-breadcrumb")
        words.append(self.cabinet_breadcrumb)
        self.cabinet_title = heading("Кабинет")
        words.append(self.cabinet_title)
        self.cabinet_caption = label("", "caption", True)
        self.cabinet_caption.set_visible(False)
        words.append(self.cabinet_caption)
        self.cabinet_header.append(words)
        self.cabinet_refresh_button = self.support_icon_button("view-refresh-symbolic", "Обновить данные", lambda _b: self._cabinet_refresh() if self._cabinet_refresh and not self.cabinet_busy else None)
        self.cabinet_refresh_button.set_visible(False)
        self.cabinet_header.append(self.cabinet_refresh_button)
        page.append(self.cabinet_header)
        self.cabinet_content = box(18)
        page.append(scrolled(self.cabinet_content))
        self.stack.add_named(page, "cabinet-section")
        self.build_support_chat_page()
        self.build_account_links()

    def cabinet_nav_row(self, title, caption, icon, callback, danger=False):
        item = Gtk.Button()
        item.add_css_class("nav-row")
        item.set_hexpand(True)
        item.update_property([Gtk.AccessibleProperty.LABEL], [title])
        item.set_tooltip_text(title)
        if danger:
            item.add_css_class("danger-action")
        row = box(11, True)
        mark = Gtk.Image.new_from_icon_name(icon)
        mark.add_css_class("nav-icon")
        mark.set_valign(Gtk.Align.CENTER)
        row.append(mark)
        text = box(3)
        text.set_hexpand(True)
        heading = label(" ".join(title.split()), "row-title", True)
        heading.set_wrap_mode(Pango.WrapMode.WORD_CHAR)
        text.append(heading)
        if caption:
            note = label(caption, "nav-caption", True)
            note.set_wrap_mode(Pango.WrapMode.WORD_CHAR)
            text.append(note)
        row.append(text)
        chevron = Gtk.Image.new_from_icon_name("go-next-symbolic")
        chevron.add_css_class("nav-chevron")
        row.append(chevron)
        item.set_child(row)
        item.connect("clicked", callback)
        return item

    def cabinet_nav_group(self, title, rows):
        group = box(7)
        group.append(heading(title, level=2, style="section-title"))
        items = box(0, style="nav-group")
        for index, row in enumerate(rows):
            if index:
                items.append(Gtk.Separator())
            items.append(self.cabinet_nav_row(*row))
        group.append(items)
        return group

    def build_account_links(self):
        while child := self.account_links.get_first_child():
            self.account_links.remove(child)
        self.account_links.set_visible(bool(self.session))
        if not self.session:
            return
        account = self.session.get("account") or {}
        features = account.get("features") or {}
        identity = self.session.get("identity") or {}
        access = []
        if identity.get("kind") != "owner":
            access.append(("Мои устройства", "Ссылки подключения и доступ устройств", "computer-symbolic", self.open_devices))
        access.append(("Счета и оплаты", "Платежи и реквизиты", "view-list-symbolic", lambda _b: self.open_cabinet_section("invoices")))
        if features.get("support"):
            access.append(("Поддержка", "Текущее обращение и ответы команды", "mail-message-new-symbolic", lambda _b: self.open_support_chat()))
        if identity.get("kind") == "retail":
            access.append(("История подписки", "Начисления и списания дней", "document-open-recent-symbolic", lambda _b: self.open_cabinet_section("history")))
        if access:
            self.account_links.append(self.cabinet_nav_group("Управление", access))
        services = []
        if features.get("referral") and identity.get("kind") == "retail":
            services.append(("Пригласить друга", "Приглашение и начисленные дни", "system-users-symbolic", lambda _b: self.open_cabinet_section("referral")))
        if identity.get("kind") != "owner":
            services.append(("Прокси для Telegram", "Подключение Telegram через наши узлы", "network-server-symbolic", lambda _b: self.open_cabinet_section("tg-proxy")))
        services.extend((doc.get("label") or "Инструкция", "Откроется в браузере", "help-browser-symbolic", lambda _b, url=doc.get("url", ""): self.open_safe_url(url)) for doc in account.get("docs") or [])
        if services:
            self.account_links.append(self.cabinet_nav_group("Сервисы и инструкции", services))
        options = box(0, style="nav-group")
        if identity.get("kind") == "retail":
            paused = (account.get("subscription") or {}).get("status") == "paused"
            options.append(self.cabinet_nav_row("Возобновить подписку" if paused else "Приостановить подписку", "Дни снова будут списываться" if paused else "Сохранить дни на время перерыва", "media-playback-start-symbolic" if paused else "media-playback-pause-symbolic", lambda _b: self.confirm_subscription_pause(paused)))
        if identity.get("kind") == "retail" and not account.get("telegram"):
            if options.get_first_child():
                options.append(Gtk.Separator())
            options.append(self.cabinet_nav_row("Привязать Telegram", "Получать ответы и открывать кабинет через бота", "insert-link-symbolic", lambda _b: self.cabinet_request("POST", "telegram-link", {}, self.show_telegram_link)))
        if options.get_first_child():
            self.account_links.append(disclosure("Действия с подпиской", options))
        actions = box(0, style="nav-group")
        actions.append(self.cabinet_nav_row("Войти заново", "Повторный вход или смена аккаунта", "avatar-default-symbolic", self.show_login_form))
        actions.append(Gtk.Separator())
        actions.append(self.cabinet_nav_row("Новый код входа", "Прежний код и другие сессии перестанут работать", "dialog-password-symbolic", lambda _b: self.confirm_cabinet("Выпустить новый код?", "Прежний код и другие сессии кабинета перестанут работать. Новый код будет показан здесь.", lambda: self.cabinet_request("POST", "token", {}, self.show_new_token), accept_label="Выпустить код", danger=True)))
        actions.append(Gtk.Separator())
        actions.append(self.cabinet_nav_row("Выйти из аккаунта", "Внешние подписки останутся в приложении", "system-log-out-symbolic", lambda _b: self.confirm_cabinet("Выйти из Quantway?", "Внешние провайдеры останутся в приложении.", self.logout_account, accept_label="Выйти", danger=True), True))
        self.account_links.append(disclosure("Безопасность аккаунта", actions))
        if account.get("review_notice"):
            self.account_links.append(label(account["review_notice"], "muted", True))

    def confirm_cabinet(self, title, explanation, action, accept_label="Подтвердить", danger=False):
        context = self.account_context()
        dialog = Gtk.MessageDialog(transient_for=self.window, modal=True, message_type=Gtk.MessageType.QUESTION,
                                   buttons=Gtk.ButtonsType.NONE, text=title)
        dialog.set_property("secondary-text", explanation)
        cancel = dialog.add_button("Отмена", Gtk.ResponseType.CANCEL)
        accept = dialog.add_button(accept_label, Gtk.ResponseType.ACCEPT)
        if accept.get_parent() is not None:
            accept.get_parent().add_css_class("workspace")
        accept.add_css_class("danger-action" if danger else "primary")
        dialog.set_default_response(Gtk.ResponseType.CANCEL)
        def response(d, value):
            d.destroy()
            if value == Gtk.ResponseType.ACCEPT and self.account_callback_current(context):
                action()
        dialog.connect("response", response)
        dialog.present()
        cancel.grab_focus()

    def cabinet_request(self, method, path, body=None, done=None):
        context = self.account_context()
        if not context or self.cabinet_busy:
            return False
        self.cabinet_busy = True
        self.cabinet_serial += 1
        serial = self.cabinet_serial
        expected_page = None if path in ("logout", "token", "subscription/pause", "subscription/resume") else self.stack.get_visible_child_name()
        self.cabinet_content.set_sensitive(False)
        self.cabinet_refresh_button.set_sensitive(False)
        self.account_links.set_sensitive(False)
        self.plans_box.set_sensitive(False)
        self.status.set_text("Обращаемся к сервису…")
        def worker():
            try:
                data = self.call_helper("cabinet", {"cookie": context[1], "method": method, "path": path, "body": body or {}})["data"]
                error = ""
            except (OSError, subprocess.TimeoutExpired, ValueError, KeyError) as exc:
                data, error = {}, str(exc) if isinstance(exc, ValueError) else "Не удалось связаться с сервисом. Попробуйте ещё раз."
            GLib.idle_add(self.cabinet_response, context, serial, data, error, done, expected_page)
        threading.Thread(target=worker, daemon=True).start()
        return True

    def cabinet_response(self, context, serial, data, error, done, expected_page=None):
        if not self.account_callback_current(context) or serial != self.cabinet_serial:
            return False
        self.cabinet_busy = False
        self.cabinet_content.set_sensitive(True)
        self.cabinet_refresh_button.set_sensitive(bool(self._cabinet_refresh))
        self.account_links.set_sensitive(True)
        self.plans_box.set_sensitive(True)
        if expected_page and expected_page != self.stack.get_visible_child_name():
            if self.status.get_text() == "Обращаемся к сервису…":
                self.status.set_text("")
            return False
        if error:
            self.status.set_text(error)
            if self._cabinet_loading:
                self.show_cabinet_state("Не удалось загрузить данные", error, retry=self.cabinet_retry)
            return False
        self.status.set_text(data.get("message") or "")
        if done:
            done(data)
        return False

    def section_body(self, title, caption="", refresh=None, back=None, back_label="Назад в кабинет"):
        self.cabinet_title.set_text(title)
        self.cabinet_caption.set_text(caption)
        self.cabinet_caption.set_visible(bool(caption))
        self._cabinet_refresh = refresh
        self._cabinet_back = back
        self._cabinet_loading = False
        self.cabinet_back_button.set_tooltip_text(back_label)
        self.cabinet_back_button.update_property([Gtk.AccessibleProperty.LABEL], [back_label])
        self.cabinet_refresh_button.set_visible(bool(refresh))
        self.cabinet_refresh_button.set_sensitive(bool(refresh) and not self.cabinet_busy)
        while child := self.cabinet_content.get_first_child():
            self.cabinet_content.remove(child)
        return self.cabinet_content

    def show_cabinet_state(self, title, caption="", loading=False, retry=None):
        while child := self.cabinet_content.get_first_child():
            self.cabinet_content.remove(child)
        self._cabinet_loading = loading
        state = box(10, style="empty-state")
        if loading:
            spinner = Gtk.Spinner()
            spinner.start()
            state.append(spinner)
        state.append(label(title, "section-title", True))
        if caption:
            state.append(label(caption, "muted", True))
        if retry:
            state.add_css_class("inline-error")
            state.append(button("Повторить", lambda _b: retry(), "primary"))
        self.cabinet_content.append(state)

    def open_cabinet_section(self, section, cursor=""):
        if section == "support":
            return self.open_support_chat()
        if not self.session or self.cabinet_busy:
            return
        title = {"invoices": "Счета и оплаты", "history": "История подписки", "support": "Поддержка",
                 "referral": "Пригласить друга", "tg-proxy": "Прокси для Telegram"}[section]
        self.cabinet_retry = lambda: self.open_cabinet_section(section, cursor)
        self.section_body(title, refresh=self.cabinet_retry)
        self.show_cabinet_state("Загружаем данные…", loading=True)
        self.stack.set_visible_child_name("cabinet-section")
        path = section + ("?before=" + quote(str(cursor), safe="") if cursor else "")
        self.cabinet_request("GET", path, done=lambda data: self.render_cabinet_section(section, data))

    def render_cabinet_section(self, section, data):
        if section == "support":
            return self.render_support(data, None)
        titles = {"invoices": "Счета и оплаты", "history": "История подписки", "support": "Поддержка",
                  "referral": "Пригласить друга", "tg-proxy": "Прокси для Telegram"}
        captions = {"invoices": "Оплата подписки и дополнительных мест", "history": "Начисления и списания дней",
                    "referral": "Делитесь приглашением и получайте дни", "tg-proxy": "Отдельное подключение для Telegram"}
        content = self.section_body(titles[section], captions.get(section, ""), refresh=lambda: self.open_cabinet_section(section))
        self.stack.set_visible_child_name("cabinet-section")
        if section == "invoices":
            invoices = data.get("invoices") or []
            pending = [item for item in invoices if item.get("state") == "awaiting"]
            archive = [item for item in invoices if item.get("state") != "awaiting"]
            for caption, group in (("Ожидают оплаты", pending), ("Остальные счета" if pending else "Ваши счета", archive)):
                if not group:
                    continue
                section_rows = box(8)
                section_rows.append(heading(caption, level=2, style="section-title"))
                rows = box(0, style="data-list")
                rows.add_css_class("ledger")
                for invoice in group:
                    if rows.get_first_child():
                        rows.append(Gtk.Separator())
                    item = Gtk.Button()
                    item.add_css_class("data-row")
                    item.add_css_class("ledger-row")
                    item.set_hexpand(True)
                    title = invoice.get("title") or "Счёт"
                    item.update_property([Gtk.AccessibleProperty.LABEL], ["Открыть счёт: " + title])
                    row = box(14, True)
                    words = box(5)
                    words.set_hexpand(True)
                    name = label(title, "row-title", True)
                    words.append(name)
                    words.append(label(invoice.get("date") or "", "caption", True))
                    row.append(words)
                    values = box(5)
                    values.set_valign(Gtk.Align.CENTER)
                    amount = label(invoice.get("amount_rub") or self.invoice_amount(invoice), "row-value", True)
                    amount.set_xalign(1)
                    values.append(amount)
                    values.append(self.invoice_status(invoice))
                    row.append(values)
                    row.append(Gtk.Image.new_from_icon_name("go-next-symbolic"))
                    item.set_child(row)
                    item.connect("clicked", lambda _b, value=invoice["id"]: self.cabinet_request("GET", "invoices/" + value, done=self.show_account_invoice))
                    rows.append(item)
                section_rows.append(rows)
                content.append(section_rows)
            if not data.get("invoices"):
                self.show_cabinet_state("У вас пока нет счетов", "Счёт появится здесь после выбора тарифа или покупки места для устройства.")
        elif section == "history":
            rows = box(0, style="data-list")
            rows.add_css_class("ledger")
            for move in data.get("moves") or []:
                if rows.get_first_child():
                    rows.append(Gtk.Separator())
                row = box(14, True, "data-row")
                row.add_css_class("ledger-row")
                words = box(5)
                words.set_hexpand(True)
                words.append(label(move.get("what") or "Движение по подписке", "row-title", True))
                words.append(label(move.get("when") or "", "caption", True))
                row.append(words)
                delta = int(move.get("delta") or 0)
                value = label(("+" if delta > 0 else "−") + self.format_days(abs(delta)) if delta else "—", "row-value", True)
                value.set_xalign(1)
                if delta > 0:
                    value.add_css_class("success")
                row.append(value)
                rows.append(row)
            content.append(rows)
            if not data.get("moves"):
                self.show_cabinet_state("История пока пуста", "Здесь появятся начисления и списания дней вашей подписки.")
        elif section == "referral":
            invitation = box(12, style="referral-card")
            invitation.add_css_class("panel")
            invitation.append(heading("Ваше приглашение", level=2, style="section-title"))
            invite = "\n".join(filter(None, (data.get("share_text"), data.get("link"))))
            copy = button("Скопировать приглашение", lambda _b: self.copy_cabinet_text(invite), "primary")
            copy.set_sensitive(bool(invite))
            if data.get("share_text"):
                text = label(data["share_text"], wrap=True)
                text.set_selectable(True)
                invitation.append(text)
            if data.get("link"):
                link = label(data["link"], "muted", True)
                link.set_wrap_mode(Pango.WrapMode.WORD_CHAR)
                link.set_selectable(True)
                invitation.append(link)
            if not invite:
                invitation.append(label("Приглашение пока недоступно. Обновите данные и попробуйте снова.", "muted", True))
            invitation.append(action_row(copy))
            content.append(invitation)
            stats = Gtk.FlowBox()
            stats.add_css_class("stat-strip")
            stats.set_selection_mode(Gtk.SelectionMode.NONE)
            stats.set_homogeneous(True)
            stats.set_min_children_per_line(1)
            stats.set_max_children_per_line(3)
            stats.set_row_spacing(12)
            stats.set_column_spacing(24)
            for title, key in (("Приглашено", "invited"), ("Бонусных приглашений", "paid"), ("Начислено дней", "days_earned")):
                stat = box(5, style="metric-cell")
                stat.append(label(str(data[key]) if data.get(key) is not None else "—", "stat-value"))
                stat.append(label(title, "caption", True))
                stats.append(stat)
                stat.get_parent().set_focusable(False)
            content.append(stats)
            if data.get("per_invite") is not None:
                content.append(label("За бонусное приглашение: +" + self.format_days(data["per_invite"]), "caption", True))
            if data.get("left") is not None:
                content.append(label("Бонусных приглашений осталось: " + str(data["left"]), "caption", True))
        elif section == "tg-proxy":
            states = {"ready": "Прокси готов к подключению", "grace": "Прокси доступен временно", "unpaid": "Для подключения продлите подписку", "unavailable": "Прокси пока недоступен"}
            state = box(5)
            state.append(heading(states.get(data.get("state"), "Прокси для Telegram"), level=2, style="section-title"))
            if data.get("until_text"):
                state.append(label(data["until_text"], "caption", True))
            content.append(state)
            rows = box(0, style="data-list")
            rows.add_css_class("ledger")
            for link in data.get("links") or []:
                if rows.get_first_child():
                    rows.append(Gtk.Separator())
                row = box(12, True, "data-row")
                row.add_css_class("ledger-row")
                host = label(link.get("host") or "Telegram", "row-title", True)
                host.set_hexpand(True)
                host.set_max_width_chars(28)
                row.append(host)
                actions = action_row(
                    button("Открыть в Telegram", lambda _b, url=link.get("url") or link.get("tg", ""): self.open_safe_url(url, telegram=True)),
                    self.support_icon_button("edit-copy-symbolic", "Скопировать ссылку", lambda _b, value=link.get("url") or link.get("tg", ""): self.copy_cabinet_text(value)))
                actions.set_hexpand(False)
                row.append(actions)
                rows.append(row)
            content.append(rows)
            if data.get("links"):
                controls = box(8)
                controls.append(label("Перевыпуск отключит ранее выданные ссылки прокси.", "caption", True))
                controls.append(button("Обновить ключи прокси", lambda _b: self.confirm_cabinet("Обновить ключи прокси?", "Ранее выданные ссылки прокси перестанут работать.", lambda: self.cabinet_request("POST", "tg-proxy/rotate", {}, lambda result: self.render_cabinet_section("tg-proxy", result)), accept_label="Обновить ключи", danger=True), "danger-action"))
                content.append(disclosure("Управление ключами", controls))
        if data.get("more") and data.get("next_before"):
            content.append(button("Следующая страница", lambda _b: self.open_cabinet_section(section, data["next_before"])))

    def show_account_invoice(self, data):
        invoice = data.get("invoice") or {}
        refresh = lambda: self.cabinet_request("GET", "invoices/" + invoice["id"], done=self.show_account_invoice)
        content = self.section_body("Оплата счёта", refresh=refresh if invoice.get("id") else None, back=lambda: self.open_cabinet_section("invoices"), back_label="Назад к счетам")
        self.stack.set_visible_child_name("cabinet-section")
        summary = box(16, style="panel")
        summary.add_css_class("invoice-summary")
        summary_header = box(12, True)
        title = heading(invoice.get("title") or "Оплата подписки", level=2, style="section-title")
        title.set_hexpand(True)
        summary_header.append(title)
        summary_header.append(self.invoice_status(invoice))
        summary.append(summary_header)
        summary.append(label(self.invoice_amount(invoice), "invoice-amount", True))
        explanations = {"awaiting": "Откройте страницу оплаты. Затем вернитесь в приложение и проверьте статус.",
                        "paid": "Оплата получена. Подписка обновится в кабинете.",
                        "review": "Платёж проверяется. Вы можете обновить его статус.",
                        "declined": "Платёж не подтверждён. Подробности доступны в реквизитах счёта.",
                        "expired": "Срок оплаты этого счёта истёк."}
        if invoice.get("state") in explanations:
            summary.append(label(explanations[invoice["state"]], "muted", True))
        dates = box(5)
        if invoice.get("deadline") and invoice.get("state") == "awaiting":
            dates.append(label("Оплатить до " + str(invoice["deadline"]), "caption", True))
        if invoice.get("date"):
            dates.append(label("Создан " + str(invoice["date"]), "caption", True))
        if dates.get_first_child():
            summary.append(dates)
        content.append(summary)
        invoice_actions = []
        if invoice.get("state") == "awaiting" and invoice.get("pay_url"):
            invoice_actions.append(button("Перейти к оплате", lambda _b: self.open_safe_url(invoice["pay_url"]), "primary"))
        if invoice.get("id"):
            self.invoice_check_button = button("Проверить оплату", lambda _b: refresh())
            if invoice.get("state") == "review":
                self.invoice_check_button.add_css_class("primary")
            invoice_actions.append(self.invoice_check_button)
        if invoice.get("state") == "paid":
            invoice_actions.insert(0, button("Вернуться в кабинет", lambda _b: self.stack.set_visible_child_name("account"), "primary"))
        if invoice_actions:
            summary.append(action_row(*invoice_actions))
        details = box(9)
        for caption, value in (("Способ оплаты", "Картой или СБП" if invoice.get("is_fiat") else "Биткоином"), ("Адрес для оплаты", invoice.get("address"))):
            if value:
                details.append(label(caption, "caption"))
                text = label(str(value), wrap=True)
                text.set_wrap_mode(Pango.WrapMode.WORD_CHAR)
                text.set_selectable(True)
                details.append(text)
                if caption == "Адрес для оплаты":
                    details.append(button("Скопировать адрес", lambda _b, address=str(value): self.copy_cabinet_text(address)))
        content.append(disclosure("Реквизиты и подробности", details))
        if invoice.get("state") == "paid":
            threading.Thread(target=self.refresh_account_worker, args=(self.account_context(),), daemon=True).start()

    @staticmethod
    def invoice_amount(invoice):
        if invoice.get("is_fiat"):
            return str(invoice.get("amount_rub") or "—")
        return str(invoice["amount_btc"]) + " BTC" if invoice.get("amount_btc") else str(invoice.get("amount_rub") or "—")

    @staticmethod
    def invoice_status(invoice):
        status = label(invoice.get("status") or "Статус не указан", "status-pill", True)
        status.set_halign(Gtk.Align.START)
        status.set_max_width_chars(20)
        status.set_wrap_mode(Pango.WrapMode.WORD_CHAR)
        state = invoice.get("state")
        if state == "paid":
            status.add_css_class("success")
        elif state in ("awaiting", "review"):
            status.add_css_class("warning")
        elif state == "declined":
            status.add_css_class("danger")
        return status

    def on_device_invoice(self, data):
        self.show_account_invoice(data)

    def confirm_subscription_pause(self, resume):
        action = "resume" if resume else "pause"
        self.confirm_cabinet("Возобновить подписку?" if resume else "Приостановить подписку?",
                             "Доступ через Quantway будет возобновлён." if resume else "Подключения устройств Quantway будут приостановлены.",
                             lambda: self.cabinet_request("POST", "subscription/" + action, {}, lambda data: self.subscription_action_done(data, resume)), accept_label="Возобновить" if resume else "Приостановить", danger=not resume)

    def subscription_action_done(self, data, resume=True):
        source = self.active_source or {}
        owned = {device.get("id") for device in (self.session or {}).get("devices") or []}
        owned.add((self.session or {}).get("current_device"))
        if not resume and source.get("kind") == "quantway" and source.get("id") in owned:
            self.on_account_device_changed("block", {}, {"id": source["id"]})
        self.status.set_text(data.get("message") or "Подписка обновлена.")
        if self.session:
            threading.Thread(target=self.refresh_account_worker, args=(self.account_context(),), daemon=True).start()

    def append_promo_controls(self, plans):
        if not ((self.session or {}).get("account", {}).get("features") or {}).get("promo"):
            return
        promo = plans.get("promo") or {}
        target = getattr(self, "promo_container", None)
        if target is None or not target.is_ancestor(self.plans_box):
            target = self.plans_box
        controls = box(9)
        if promo.get("code"):
            controls.append(label("Промокод: " + promo["code"], "muted", True))
            controls.append(button("Убрать промокод", lambda _b: self.cabinet_request("DELETE", "promo", {}, lambda _d: self.load_plans(None)), "flat"))
        entry = Gtk.Entry()
        entry.set_max_length(128)
        controls.append(field("_Промокод", entry))
        def apply(_b):
            value = entry.get_text().strip()
            if value:
                self.cabinet_request("POST", "promo", {"code": value}, lambda _d: self.load_plans(None))
            else:
                self.status.set_text("Введите промокод.")
        entry.connect("activate", apply)
        controls.append(button("Применить промокод", apply))
        target.append(disclosure("Промокод применён" if promo.get("code") else "Промокод", controls, expanded=bool(promo.get("code"))))

    def render_support(self, data, content):
        # Keep the existing fixture/response entry point while all support UI
        # lives on its dedicated chat page.
        self._support_context = self.account_context()
        self._support_ticket_id = None
        self.stack.set_visible_child_name("support-chat")
        self.render_support_chat(data)

    def show_support_ticket(self, data):
        self._support_context = self.account_context()
        self._support_ticket_id = (data.get("ticket") or {}).get("id") or self._support_ticket_id or 0
        self.stack.set_visible_child_name("support-chat")
        self.render_support_chat(data, self._support_ticket_id)

    def append_messages(self, content, messages):
        for message in messages:
            content.append(self.support_message_bubble(message))

    def choose_support_image(self, buffer):
        context = self.account_context()
        chooser = Gtk.FileChooserNative.new("Изображение для поддержки", self.window, Gtk.FileChooserAction.OPEN, "Выбрать", "Отмена")
        filter_ = Gtk.FileFilter()
        filter_.set_name("PNG и JPEG")
        filter_.add_mime_type("image/png")
        filter_.add_mime_type("image/jpeg")
        chooser.add_filter(filter_)
        def response(dialog, value):
            file = dialog.get_file() if value == Gtk.ResponseType.ACCEPT else None
            dialog.destroy()
            if not file or not self.account_callback_current(context):
                return
            try:
                with open(file.get_path(), "rb") as stream:
                    image = stream.read((10 << 20) + 1)
                if len(image) > 10 << 20:
                    raise ValueError()
            except (OSError, TypeError, ValueError):
                self.status.set_text("Выберите PNG или JPEG размером до 10 МБ.")
                return
            draft = buffer.get_text(buffer.get_start_iter(), buffer.get_end_iter(), False)
            caption = draft.strip()
            self.confirm_cabinet("Отправить изображение в поддержку?", "Выбранный файл и текст сообщения будут отправлены поддержке Quantway.",
                                 lambda: self.send_support_image(image, caption, draft), accept_label="Отправить изображение")
        chooser.connect("response", response)
        chooser.show()

    def show_support_image(self, data):
        try:
            raw = base64.b64decode(data["image_base64"], validate=True)
            loader = GdkPixbuf.PixbufLoader.new()
            def prepare_size(decoder, width, height):
                scale = min(1, 900 / max(1, width), 650 / max(1, height))
                decoder.set_size(max(1, round(width * scale)), max(1, round(height * scale)))
            loader.connect("size-prepared", prepare_size)
            loader.write(raw)
            loader.close()
            picture = Gtk.Picture.new_for_pixbuf(loader.get_pixbuf())
            picture.set_can_shrink(True)
            if hasattr(picture, "set_content_fit"):
                picture.set_content_fit(Gtk.ContentFit.CONTAIN)
            else:
                picture.set_keep_aspect_ratio(True)
        except (ValueError, KeyError, GLib.Error):
            self.status.set_text("Не удалось открыть изображение.")
            return
        window = Gtk.Window(title="Изображение из поддержки", transient_for=self.window, modal=True, default_width=800, default_height=600)
        window.set_child(picture)
        window.present()

    def show_telegram_link(self, data):
        content = self.section_body("Привязка Telegram")
        self.stack.set_visible_child_name("cabinet-section")
        card = box(14, style="panel")
        card.append(heading("Подключите Telegram к аккаунту", level=2, style="section-title"))
        card.append(label("Откройте бота Quantway и завершите привязку. Затем вернитесь в приложение.", "muted", True))
        content.append(card)
        for key in ("url", "link", "telegram_url"):
            if data.get(key):
                card.append(action_row(
                    button("Открыть Telegram", lambda _b, url=data[key]: self.open_safe_url(url, telegram=True), "primary"),
                    button("Скопировать ссылку", lambda _b, url=data[key]: self.copy_cabinet_text(url))))
                break
        else:
            card.append(label(data.get("message") or "Ссылка недоступна. Обновите кабинет и попробуйте снова.", wrap=True))

    def show_new_token(self, data):
        code = data.get("code") or ""
        fresh_cookie = data.get("session_cookie")
        if fresh_cookie:
            session = dict(self.session)
            self.forget_saved_account()
            self.reset_account_requests()
            session["cookie"] = fresh_cookie
            self.session = session
            self.render_account(session)
            self.persist_account_session(session)
        else:
            self.show_login_form(None)
        content = self.section_body("Новый код входа")
        self.stack.set_visible_child_name("cabinet-section")
        card = box(16, style="panel")
        card.append(heading("Сохраните новый код", level=2, style="section-title"))
        card.append(label("Прежний код больше не действует. Этот код даёт доступ к вашему аккаунту.", "muted", True))
        text = label(code, "token-code", True)
        text.set_wrap_mode(Pango.WrapMode.WORD_CHAR)
        text.set_max_width_chars(36)
        text.set_selectable(True)
        card.append(text)
        card.append(action_row(
            button("Скопировать код", lambda _b: self.copy_cabinet_text(code), "primary"),
            button("Вернуться в кабинет" if fresh_cookie else "Войти с новым кодом", lambda _b: self.stack.set_visible_child_name("account"))))
        content.append(card)

    def logout_account(self):
        # This API only clears a browser cookie; local logout must also work
        # offline. Never leave a saved login active while waiting on the server.
        cookie = (self.session or {}).get("cookie")
        self.finish_account_logout()
        if cookie:
            def worker():
                try:
                    self.call_helper("cabinet", {"cookie": cookie, "method": "POST", "path": "logout", "body": {}})
                except (OSError, ValueError, subprocess.TimeoutExpired):
                    pass
            threading.Thread(target=worker, daemon=True).start()

    def finish_account_logout(self):
        self.show_login_form(None)
        self.stack.set_visible_child_name("account")
        self.status.set_text("Вы вышли из Quantway.")

    def copy_cabinet_text(self, text):
        Gdk.Display.get_default().get_clipboard().set(str(text))
        self.status.set_text("Скопировано.")

    def open_safe_url(self, url, telegram=False):
        try:
            parsed = urlparse(url)
        except ValueError:
            self.status.set_text("Не удалось открыть ссылку.")
            return
        allowed = parsed.scheme == "https" and bool(parsed.hostname) and not parsed.username and not parsed.password
        if telegram:
            allowed = allowed or parsed.scheme == "tg" and parsed.netloc in ("resolve", "proxy")
        if not allowed:
            self.status.set_text("Не удалось открыть ссылку.")
            return
        try:
            Gio.AppInfo.launch_default_for_uri(url, None)
        except GLib.Error:
            self.status.set_text("Не удалось открыть приложение. Скопируйте ссылку и откройте её вручную.")
