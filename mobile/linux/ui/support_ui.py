"""Native support chat with an independent viewport and persistent composer."""
import base64
import subprocess
import threading

from gi.repository import Gdk, GLib, Gtk, Pango

from desktop_ui import box, button, heading, label, scrolled


class SupportChatMixin:
    def init_support_chat_state(self):
        self._support_context = None
        self._support_serial = 0
        self._support_busy = False
        self._support_quiet = False
        self._support_timer = None
        self._support_ticket_id = None
        self._support_messages = None
        self._support_history = []
        self._support_max_runes = 4000
        self._support_can_write = True
        self._support_force_bottom = True
        self._support_focus_pending = False
        self._support_bubble_layout_width = None

    @staticmethod
    def support_icon_button(icon, caption, callback):
        item = Gtk.Button(icon_name=icon)
        item.set_valign(Gtk.Align.CENTER)
        item.set_tooltip_text(caption)
        item.update_property([Gtk.AccessibleProperty.LABEL], [caption])
        item.connect("clicked", callback)
        return item

    def build_support_chat_page(self):
        page = box(0, style="chat-page")
        header = box(12, True, "chat-header")
        header.add_css_class("section-header")
        self.support_header = header
        header.append(self.support_icon_button("go-previous-symbolic", "Назад в кабинет", lambda _b: self.stack.set_visible_child_name("account")))
        words = box(4)
        words.set_hexpand(True)
        words.append(label("Кабинет", "section-breadcrumb"))
        title = heading("Поддержка")
        title.set_max_width_chars(20)
        words.append(title)
        self.support_chat_state = label("", "caption", True)
        self.support_chat_state.set_max_width_chars(30)
        words.append(self.support_chat_state)
        header.append(words)
        self.support_chat_spinner = Gtk.Spinner()
        header.append(self.support_chat_spinner)
        self.support_history_button = Gtk.MenuButton()
        history_label = box(7, True)
        history_label.append(Gtk.Image.new_from_icon_name("document-open-recent-symbolic"))
        history_label.append(label("История"))
        self.support_history_button.set_child(history_label)
        self.support_history_button.set_valign(Gtk.Align.CENTER)
        self.support_history_button.set_tooltip_text("История обращений")
        self.support_history_button.update_property([Gtk.AccessibleProperty.LABEL], ["История обращений"])
        self.support_history_popover = Gtk.Popover()
        self.support_history_list = box(4)
        self.support_history_list.add_css_class("workspace")
        history_scroll = scrolled(self.support_history_list)
        history_scroll.set_vexpand(False)
        history_scroll.set_min_content_width(260)
        history_scroll.set_max_content_width(380)
        history_scroll.set_propagate_natural_width(True)
        history_scroll.set_max_content_height(320)
        history_scroll.set_propagate_natural_height(True)
        self.support_history_popover.set_child(history_scroll)
        self.support_history_button.set_popover(self.support_history_popover)
        header.append(self.support_history_button)
        page.append(header)
        page.append(Gtk.Separator())
        self.support_messages = box(12)
        self.support_messages.add_css_class("chat-thread")
        self.support_messages.set_hexpand(True)
        self.support_messages.set_halign(Gtk.Align.FILL)
        self.support_messages.set_margin_top(18)
        self.support_messages.set_margin_bottom(18)
        self.support_messages.set_margin_start(14)
        self.support_messages.set_margin_end(14)
        self.support_messages.add_tick_callback(self.update_support_bubble_widths)
        self.support_scroll = scrolled(self.support_messages)
        self.support_scroll.add_css_class("chat-scroll")
        self.support_scroll.get_vadjustment().connect("value-changed", self.support_scroll_changed)
        page.append(self.support_scroll)
        self.support_unread_button = button("Новые сообщения", lambda _b: self.support_jump_to_latest(), "chat-unread")
        self.support_unread_button.set_halign(Gtk.Align.CENTER)
        self.support_unread_button.set_visible(False)
        page.append(self.support_unread_button)
        page.append(Gtk.Separator())
        composer = box(8, style="support-composer")
        self.support_composer = composer
        self.support_reply_context = label("Опишите, что случилось. Ответ появится в этой переписке.", "caption", True)
        self.support_reply_context.add_css_class("chat-composer-hint")
        self.support_reply_context.set_hexpand(True)
        self.support_buffer = Gtk.TextBuffer()
        self.support_text = Gtk.TextView(buffer=self.support_buffer)
        self.support_text.set_wrap_mode(Gtk.WrapMode.WORD_CHAR)
        self.support_text.set_accepts_tab(False)
        self.support_text.set_top_margin(9)
        self.support_text.set_bottom_margin(9)
        self.support_text.set_left_margin(11)
        self.support_text.set_right_margin(11)
        self.support_text.update_property([Gtk.AccessibleProperty.LABEL], ["Сообщение поддержке"])
        self.support_text.set_tooltip_text("Enter — отправить, Shift+Enter — новая строка")
        self.support_buffer.connect("changed", lambda _buffer: self.sync_support_composer())
        key = Gtk.EventControllerKey()
        key.set_propagation_phase(Gtk.PropagationPhase.CAPTURE)
        key.connect("key-pressed", self.support_key_pressed)
        self.support_text.add_controller(key)
        self.support_editor = scrolled(self.support_text)
        self.support_editor.set_hexpand(True)
        self.support_editor.set_vexpand(False)
        self.support_editor.set_min_content_height(44)
        self.support_editor.set_max_content_height(140)
        self.support_editor.set_propagate_natural_height(True)
        input_overlay = Gtk.Overlay()
        input_overlay.add_css_class("chat-editor")
        input_overlay.set_hexpand(True)
        input_overlay.set_vexpand(False)
        input_overlay.set_child(self.support_editor)
        self.support_placeholder = label("Написать сообщение", "muted")
        self.support_placeholder.set_margin_start(12)
        self.support_placeholder.set_margin_end(12)
        self.support_placeholder.set_margin_top(9)
        self.support_placeholder.set_valign(Gtk.Align.START)
        self.support_placeholder.set_can_target(False)
        input_overlay.add_overlay(self.support_placeholder)
        input_overlay.set_clip_overlay(self.support_placeholder, True)
        actions = box(8, True, "composer-actions")
        self.support_attach_button = self.support_icon_button("mail-attachment-symbolic", "Прикрепить скриншот", lambda _b: self.choose_support_image(self.support_buffer))
        self.support_attach_button.add_css_class("chat-attach")
        self.support_attach_button.set_size_request(44, 44)
        self.support_attach_button.set_valign(Gtk.Align.END)
        actions.append(self.support_attach_button)
        actions.append(input_overlay)
        self.support_character_count = label("0 / 4000", "caption", True)
        self.support_character_count.set_visible(False)
        self.support_send_button = self.support_icon_button("mail-send-symbolic", "Отправить сообщение", self.send_support_text)
        self.support_send_button.add_css_class("primary")
        self.support_send_button.add_css_class("chat-send")
        self.support_send_button.set_size_request(44, 44)
        self.support_send_button.set_valign(Gtk.Align.END)
        actions.append(self.support_send_button)
        composer.append(actions)
        composer_footer = box(12, True)
        composer_footer.append(self.support_reply_context)
        self.support_character_count.set_xalign(1)
        self.support_character_count.set_valign(Gtk.Align.START)
        composer_footer.append(self.support_character_count)
        composer.append(composer_footer)
        self.support_composer_actions = actions
        self.support_return_current = button("Перейти к текущему обращению", lambda _b: self.open_support_chat(), "primary")
        self.support_return_current.set_visible(False)
        composer.append(self.support_return_current)
        page.append(composer)
        self.stack.add_named(page, "support-chat")
        self.stack.connect("notify::visible-child-name", self.support_page_changed)
        self.sync_support_composer()

    def stop_support_poll(self):
        if self._support_timer is not None:
            GLib.source_remove(self._support_timer)
            self._support_timer = None

    def support_page_changed(self, *_args):
        if self.stack.get_visible_child_name() != "support-chat" or not self.session:
            self.stop_support_poll()
        elif self._support_timer is None:
            self._support_timer = GLib.timeout_add_seconds(8, self.support_poll)

    def support_poll(self):
        if self.closing or self.stack.get_visible_child_name() != "support-chat" or not self.account_callback_current(self._support_context) or not self.session:
            self._support_timer = None
            return False
        if not self._support_busy:
            self.refresh_support_chat(quiet=True)
        return True

    def open_support_chat(self, ticket_id=None):
        if not self.session or self._support_busy and not self._support_quiet and self._support_context == self.account_context():
            return False
        context = self.account_context()
        changed_context = self._support_context != context
        changed_ticket = self._support_ticket_id != ticket_id
        if self._support_context != context:
            self._support_serial += 1
            self._support_busy = False
            self._support_quiet = False
            self._support_context = context
            self._support_history = []
            self._support_messages = None
            self.support_buffer.set_text("")
            while child := self.support_messages.get_first_child():
                self.support_messages.remove(child)
        if self._support_ticket_id != ticket_id:
            self._support_messages = None
        self._support_ticket_id = ticket_id
        if changed_context or changed_ticket:
            self._support_force_bottom = True
        self._support_focus_pending = True
        self.stack.set_visible_child_name("support-chat")
        self.status.set_text("")
        GLib.idle_add(self.focus_support_composer, context)
        return self.refresh_support_chat()

    def focus_support_composer(self, context):
        if not self.closing and self._support_focus_pending and self._support_can_write and self.account_callback_current(context) and self._support_context == context and self.stack.get_visible_child_name() == "support-chat":
            self.support_text.grab_focus()
            self._support_focus_pending = False
        return False

    def refresh_support_chat(self, quiet=False):
        ticket_id = self._support_ticket_id
        path = "support/tickets/" + str(ticket_id) if ticket_id is not None else "support"
        if not quiet and self._support_messages is None:
            self.show_support_placeholder("Загружаем переписку…", loading=True)
        return self.support_request("GET", path, None, lambda data: self.render_support_chat(data, ticket_id), quiet=quiet)

    def support_request(self, method, path, body, done, quiet=False):
        context = self.account_context()
        if not context or self._support_busy and (not self._support_quiet or quiet) or self.stack.get_visible_child_name() != "support-chat":
            return False
        self._support_busy = True
        self._support_quiet = quiet
        self._support_serial += 1
        serial = self._support_serial
        if not quiet:
            self.support_chat_spinner.start()
        self.sync_support_composer()
        def worker():
            try:
                payload = {"cookie": context[1], "method": method, "path": path, "body": body or {}}
                data = self.call_helper("cabinet", payload)["data"]
                if not isinstance(data, dict):
                    raise ValueError("Не удалось прочитать ответ поддержки.")
                error = ""
            except (OSError, subprocess.TimeoutExpired, ValueError, KeyError) as exc:
                data = {}
                error = str(exc) if isinstance(exc, ValueError) else "Не удалось связаться с поддержкой. Попробуйте ещё раз."
            GLib.idle_add(self.support_response, context, serial, data, error, done, quiet, method == "POST")
        threading.Thread(target=worker, daemon=True).start()
        return True

    def support_response(self, context, serial, data, error, done, quiet=False, acknowledge_hidden=False):
        if not self.account_callback_current(context) or self._support_context != context or serial != self._support_serial:
            return False
        self._support_busy = False
        self._support_quiet = False
        self.support_chat_spinner.stop()
        self.sync_support_composer()
        if self.stack.get_visible_child_name() != "support-chat":
            if acknowledge_hidden and not error and done:
                done(data)
            return False
        if error:
            if not quiet or self._support_messages is None:
                self.status.set_text(error)
            if self._support_messages is None:
                self.show_support_placeholder("Не удалось загрузить переписку.", retry=True)
            return False
        if not quiet:
            self.status.set_text("")
        if done:
            done(data)
        return False

    def show_support_placeholder(self, text, loading=False, retry=False):
        while child := self.support_messages.get_first_child():
            self.support_messages.remove(child)
        state = box(10, style="empty-state")
        state.add_css_class("chat-empty")
        state.set_halign(Gtk.Align.CENTER)
        state.set_valign(Gtk.Align.CENTER)
        image = Gtk.Image.new_from_icon_name("mail-message-new-symbolic")
        image.set_pixel_size(28)
        state.append(image)
        state.append(label(text, "muted", True))
        if loading:
            spinner = Gtk.Spinner()
            spinner.start()
            state.append(spinner)
        if retry:
            state.append(button("Повторить", lambda _b: self.refresh_support_chat(), "primary"))
        self.support_messages.append(state)

    def render_support_chat(self, data, ticket_id=None):
        if ticket_id is None:
            current = data.get("current") or {}
            messages = current.get("messages") or []
            self._support_history = data.get("history") or []
            self._support_max_runes = int(data.get("max_runes") or 4000)
            self._support_can_write = True
            state = current.get("state") or "Новое обращение"
            if current.get("id"):
                state = f"Обращение №{current['id']} · {state}"
            if current.get("undeliverable"):
                self.support_reply_context.set_text("Ответы не доходят в Telegram. Читайте переписку здесь.")
            elif data.get("reopen"):
                self.support_reply_context.set_text("Новое сообщение продолжит недавно закрытое обращение.")
            else:
                self.support_reply_context.set_text("Ответ появится здесь" + (" и в Telegram." if data.get("telegram") else "."))
        else:
            ticket = data.get("ticket") or {}
            messages = data.get("messages") or []
            self._support_can_write = not ticket.get("closed") or bool(data.get("reopenable"))
            state = "Обращение №" + str(ticket.get("id") or ticket_id)
            if ticket.get("state"):
                state += " · " + str(ticket["state"])
            self.support_reply_context.set_text("Сообщение продолжит это обращение." if self._support_can_write else "Обращение закрыто. Новое сообщение можно отправить в текущей переписке.")
        self.support_chat_state.set_text(state)
        self.render_support_history()
        self.sync_support_composer()
        if self._support_focus_pending:
            if self._support_can_write:
                GLib.idle_add(self.focus_support_composer, self._support_context)
            else:
                self._support_focus_pending = False
        if self._support_messages == messages:
            if self._support_force_bottom:
                self._support_force_bottom = False
                GLib.idle_add(self.restore_support_scroll, self._support_context, True, 0)
            return
        adjustment = self.support_scroll.get_vadjustment()
        previous_position = adjustment.get_value()
        at_bottom = previous_position >= adjustment.get_upper() - adjustment.get_page_size() - 48
        old_count = len(self._support_messages or [])
        initial = self._support_messages is None
        self._support_messages = messages
        while child := self.support_messages.get_first_child():
            self.support_messages.remove(child)
        if not messages:
            self.show_support_placeholder("Пока нет сообщений. Опишите проблему в поле ниже.")
        for message in messages:
            self.support_messages.append(self.support_message_bubble(message))
        follow = self._support_force_bottom or at_bottom or initial
        self._support_force_bottom = False
        if not follow and len(messages) > old_count:
            self.support_unread_button.set_label("Новые сообщения")
            self.support_unread_button.set_visible(True)
        GLib.idle_add(self.restore_support_scroll, self._support_context, follow, previous_position)

    def support_message_bubble(self, message):
        author = str(message.get("author") or "")
        staff = bool(message["from_us"]) if "from_us" in message else any(word in author.lower() for word in ("поддерж", "quantway"))
        bubble = box(7, style="chat-bubble")
        bubble.add_css_class("from-us" if staff else "from-user")
        bubble.set_halign(Gtk.Align.START if staff else Gtk.Align.END)
        bubble.set_hexpand(False)
        meta = box(10, True)
        name = label(author or ("Поддержка" if staff else "Вы"), "chat-author", True)
        name.set_wrap_mode(Pango.WrapMode.WORD_CHAR)
        name.set_max_width_chars(20)
        meta.append(name)
        at = label(str(message.get("at") or ""), "chat-meta", True)
        at.set_wrap_mode(Pango.WrapMode.WORD_CHAR)
        at.set_max_width_chars(16)
        at.set_halign(Gtk.Align.END)
        meta.append(at)
        bubble.append(meta)
        text = label(str(message.get("body") or ""), wrap=True)
        text.set_wrap_mode(Pango.WrapMode.WORD_CHAR)
        text.add_css_class("chat-message-content")
        text.set_max_width_chars(52)
        text.set_hexpand(False)
        text.set_selectable(True)
        if text.get_text():
            bubble.append(text)
        if message.get("note") and not (message["note"] == "(изображение)" and message.get("images")):
            bubble.append(label(str(message["note"]), "caption", True))
        for image in message.get("images") or []:
            bubble.append(button("Открыть изображение", lambda _b, image_id=image: self.support_request("GET", "support/attachments/" + str(image_id), None, self.show_support_image)))
        self.size_support_bubble(bubble, self.support_messages.get_width())
        return bubble

    @staticmethod
    def size_support_bubble(bubble, available):
        # Keep messages readable on wide windows without setting a minimum width.
        inset = max(0, available - 550)
        bubble.set_margin_start(inset if bubble.has_css_class("from-user") else 0)
        bubble.set_margin_end(inset if bubble.has_css_class("from-us") else 0)

    def update_support_bubble_widths(self, thread, _clock):
        available = thread.get_width()
        if available != self._support_bubble_layout_width:
            self._support_bubble_layout_width = available
            child = thread.get_first_child()
            while child:
                if child.has_css_class("chat-bubble"):
                    self.size_support_bubble(child, available)
                child = child.get_next_sibling()
        return True

    def restore_support_scroll(self, context, bottom, previous):
        if self.closing or not self.account_callback_current(context) or self.stack.get_visible_child_name() != "support-chat":
            return False
        adjustment = self.support_scroll.get_vadjustment()
        adjustment.set_value(max(adjustment.get_lower(), adjustment.get_upper() - adjustment.get_page_size()) if bottom else previous)
        if bottom:
            self.support_unread_button.set_visible(False)
        return False

    def support_jump_to_latest(self):
        self.restore_support_scroll(self._support_context, True, 0)

    def support_scroll_changed(self, adjustment):
        if adjustment.get_value() >= adjustment.get_upper() - adjustment.get_page_size() - 48:
            self.support_unread_button.set_visible(False)

    def render_support_history(self):
        while child := self.support_history_list.get_first_child():
            self.support_history_list.remove(child)
        def open_ticket(ticket_id):
            self.support_history_popover.popdown()
            self.open_support_chat(ticket_id)
        current = button("Текущее обращение", lambda _b: open_ticket(None), "flat")
        current.set_halign(Gtk.Align.FILL)
        self.support_history_list.append(current)
        for ticket in self._support_history:
            title = str(ticket.get("subject") or "Обращение")
            if ticket.get("closed"):
                title += " · " + str(ticket["closed"])
            item = button(title, lambda _b, value=ticket["id"]: open_ticket(value), "flat")
            item.set_halign(Gtk.Align.FILL)
            item.get_child().set_wrap(True)
            item.get_child().set_wrap_mode(Pango.WrapMode.WORD_CHAR)
            item.get_child().set_max_width_chars(35)
            self.support_history_list.append(item)
        self.support_history_button.set_sensitive(bool(self._support_history or self._support_ticket_id is not None) and not (self._support_busy and not self._support_quiet))

    def support_draft(self):
        return self.support_buffer.get_text(self.support_buffer.get_start_iter(), self.support_buffer.get_end_iter(), False)

    def sync_support_composer(self):
        if not hasattr(self, "support_send_button"):
            return
        text = self.support_draft().strip()
        blocking = self._support_busy and not self._support_quiet
        self.support_send_button.set_sensitive(not blocking and self._support_can_write and bool(text) and len(text) <= self._support_max_runes)
        images = bool(((self.session or {}).get("account", {}).get("features") or {}).get("images"))
        self.support_attach_button.set_visible(images)
        self.support_attach_button.set_sensitive(not blocking and self._support_can_write and len(text) <= self._support_max_runes)
        self.support_editor.set_visible(self._support_can_write)
        self.support_composer_actions.set_visible(self._support_can_write)
        self.support_return_current.set_visible(not self._support_can_write)
        self.support_character_count.set_text(f"{len(text)} / {self._support_max_runes}")
        self.support_character_count.set_visible(self._support_can_write and len(text) >= self._support_max_runes * 0.9)
        self.support_placeholder.set_visible(not bool(self.support_draft()))
        if hasattr(self, "support_history_button"):
            self.support_history_button.set_sensitive(not blocking and bool(self._support_history or self._support_ticket_id is not None))

    def support_key_pressed(self, _controller, keyval, _keycode, state):
        if keyval in (Gdk.KEY_Return, Gdk.KEY_KP_Enter) and not state & Gdk.ModifierType.SHIFT_MASK:
            if self.support_draft().strip():
                self.send_support_text(None)
            return True
        return False

    def send_support_text(self, _button):
        raw = self.support_draft()
        text = raw.strip()
        if not text or len(text) > self._support_max_runes:
            self.status.set_text(f"Введите сообщение длиной до {self._support_max_runes} символов.")
            return False
        if not self._support_can_write:
            return False
        accepted = self.support_request("POST", "support/messages", {"body": text}, lambda data: self.support_message_sent(data, raw))
        if accepted and self.stack.get_visible_child_name() == "support-chat":
            self.support_text.grab_focus()
        return accepted

    def support_message_sent(self, _data, sent_draft):
        if self.support_draft() == sent_draft:
            self.support_buffer.set_text("")
        self._support_force_bottom = True
        self._support_ticket_id = None
        if self.stack.get_visible_child_name() == "support-chat":
            self.refresh_support_chat()

    def send_support_image(self, image, caption, sent_draft):
        if len(caption) > self._support_max_runes:
            self.status.set_text(f"Подпись должна быть не длиннее {self._support_max_runes} символов.")
            return False
        return self.support_request("POST", "support/images", {"image_base64": base64.b64encode(image).decode("ascii"), "caption": caption},
                                    lambda data: self.support_message_sent(data, sent_draft))
