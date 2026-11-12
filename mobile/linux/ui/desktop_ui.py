"""Native GTK desktop layout, separate from account and VPN operations."""
from pathlib import Path
from platform_runtime import platform_label, brand_image

import gi
gi.require_version("Gtk", "4.0")
gi.require_version("Gdk", "4.0")
gi.require_version("GdkPixbuf", "2.0")
from gi.repository import Gdk, GdkPixbuf, Gtk, Pango


class StatusMessage:
    """Keep the full message separate from its bounded on-screen preview."""

    def __init__(self, on_change):
        self._text = ""
        self._kind = None
        self._on_change = on_change

    def set_text(self, text, kind=None):
        self._text = text
        self._kind = kind
        self._on_change()

    def get_text(self):
        return self._text

    def get_kind(self):
        return self._kind


def label(text="", style=None, wrap=False, *, accessible_role=None):
    properties = {"label": text, "xalign": 0}
    if accessible_role is not None:
        properties["accessible_role"] = accessible_role
    return _configure_label(Gtk.Label(**properties), style, wrap)


def _configure_label(widget, style, wrap):
    if style:
        widget.add_css_class(style)
    widget.set_wrap(wrap)
    if wrap:
        widget.set_wrap_mode(Pango.WrapMode.WORD_CHAR)
        widget.set_max_width_chars(65)
    return widget


def heading(text, level=1, style=None, wrap=True):
    """Give page/section headings their structure as well as their appearance."""
    if not isinstance(level, int) or isinstance(level, bool) or not 1 <= level <= 6:
        raise ValueError("Heading level must be between 1 and 6")
    widget = label(text, style or ("page-title" if level == 1 else "section-title"), wrap,
                   accessible_role=Gtk.AccessibleRole.HEADING)
    widget.update_property([Gtk.AccessibleProperty.LEVEL], [level])
    return widget


class StatusLabel(Gtk.Label):
    """Repeated telemetry polls must not re-announce unchanged lifecycle text."""

    def set_text(self, text):
        if text != self.get_text():
            Gtk.Label.set_text(self, text)


def status_label(text="", style=None, wrap=True, *, urgent=False):
    """Opt in important lifecycle messages; telemetry and peer badges use label()."""
    widget = StatusLabel(label=text, xalign=0,
                         accessible_role=Gtk.AccessibleRole.ALERT if urgent else Gtk.AccessibleRole.STATUS)
    return _configure_label(widget, style, wrap)


def box(spacing=12, horizontal=False, style=None):
    widget = Gtk.Box(orientation=Gtk.Orientation.HORIZONTAL if horizontal else Gtk.Orientation.VERTICAL, spacing=spacing)
    if style:
        widget.add_css_class(style)
    return widget


def button(text, callback, style=None):
    widget = Gtk.Button(label=text)
    caption = widget.get_child()
    caption.set_wrap(True)
    # Action labels may wrap between words, never into isolated letters.
    # User-provided menu subjects opt into WORD_CHAR at their own call site.
    caption.set_wrap_mode(Pango.WrapMode.WORD)
    caption.set_max_width_chars(36)
    caption.set_justify(Gtk.Justification.CENTER)
    if style:
        widget.add_css_class(style)
    widget.set_valign(Gtk.Align.CENTER)
    widget.set_halign(Gtk.Align.START)
    widget.connect("clicked", callback)
    return widget


def disclosure(title, child, expanded=False):
    """Give every native disclosure a measured header and separated content."""
    widget = Gtk.Expander()
    widget.add_css_class("disclosure")
    heading = label(title, "disclosure-title", True)
    heading.set_hexpand(True)
    heading.set_margin_top(8)
    heading.set_margin_bottom(8)
    widget.set_label_widget(heading)
    child.set_margin_top(max(8, child.get_margin_top()))
    child.set_margin_bottom(max(4, child.get_margin_bottom()))
    widget.set_child(child)
    widget.set_expanded(expanded)
    return widget


class ActionRow(Gtk.FlowBox):
    """Native wrapping layout with predictable Tab traversal of its controls."""

    def do_focus(self, direction):
        if direction not in (Gtk.DirectionType.TAB_FORWARD, Gtk.DirectionType.TAB_BACKWARD):
            return Gtk.FlowBox.do_focus(self, direction)
        root = self.get_root()
        focused = root.get_focus() if root is not None else None
        items = []
        wrapper = self.get_first_child()
        while wrapper is not None:
            item = wrapper.get_child()
            if item is not None:
                items.append(item)
            wrapper = wrapper.get_next_sibling()
        current = next((i for i, item in enumerate(items)
                        if focused is item or (focused is not None and focused.is_ancestor(item))), None)
        step = 1 if direction == Gtk.DirectionType.TAB_FORWARD else -1
        start = (current + step if current is not None else 0 if step == 1 else len(items) - 1)
        for i in range(start, len(items) if step == 1 else -1, step):
            item = items[i]
            if (item.get_visible() and item.get_parent().get_visible() and item.is_sensitive() and
                    item.child_focus(direction)):
                return True
        # GTK's FlowBox can otherwise retain its current child when a sibling
        # is disabled. Let the surrounding page continue at either boundary.
        return False


def action_row(*items):
    """Native wrapping toolbar; the controls keep their own keyboard stops."""
    row = ActionRow()
    row.set_selection_mode(Gtk.SelectionMode.NONE)
    row.set_homogeneous(False)
    row.set_halign(Gtk.Align.FILL)
    row.set_hexpand(True)
    row.set_row_spacing(8)
    row.set_column_spacing(10)
    row.set_min_children_per_line(1)
    row.set_max_children_per_line(max(1, len(items)))
    for item in items:
        row.append(item)
        child = item.get_parent()
        child.set_focusable(False)
        child.set_visible(item.get_visible())
        item.connect("notify::visible", lambda widget, _p, wrapper=child: wrapper.set_visible(widget.get_visible()))
    return row


def scrolled(child):
    widget = Gtk.ScrolledWindow()
    widget.set_policy(Gtk.PolicyType.NEVER, Gtk.PolicyType.AUTOMATIC)
    widget.set_child(child)
    widget.set_vexpand(True)
    return widget


def icon_button(icon, caption, callback):
    widget = Gtk.Button.new_from_icon_name(icon)
    widget.set_tooltip_text(caption)
    widget.update_property([Gtk.AccessibleProperty.LABEL], [caption])
    widget.add_css_class("icon-action")
    widget.set_size_request(36, 36)
    widget.set_valign(Gtk.Align.CENTER)
    widget.connect("clicked", callback)
    return widget


def field(caption, entry):
    container = box(6)
    title = Gtk.Label.new_with_mnemonic(caption)
    title.set_xalign(0)
    title.add_css_class("caption")
    title.set_mnemonic_widget(entry)
    entry.update_property([Gtk.AccessibleProperty.LABEL], [title.get_text()])
    container.append(title)
    container.append(entry)
    return container


def progress_widgets():
    container = box(10, True, "progress-line")
    spinner = Gtk.Spinner()
    text = label("Подготавливаем проверку…", wrap=True)
    text.set_hexpand(True)
    percent = Gtk.ProgressBar()
    percent.set_show_text(True)
    percent.set_text("")
    percent.set_size_request(76, -1)
    percent.set_valign(Gtk.Align.CENTER)
    percent.update_property([Gtk.AccessibleProperty.LABEL], ["Проверка маршрутов"])
    container.append(spinner)
    container.append(text)
    container.append(percent)
    container.set_visible(False)
    return container, spinner, text, percent


def build_window(app):
    settings = Gtk.Settings.get_default()
    settings.set_property("gtk-application-prefer-dark-theme", True)
    app.window = Gtk.ApplicationWindow(application=app, title="Quantway")
    app.window.add_css_class("quantway")
    app.window.set_default_size(1060, 740)
    app.window.set_size_request(740, 580)
    app.window.connect("close-request", app.close_window)
    header = Gtk.HeaderBar()
    header.set_title_widget(Gtk.Label(label="Quantway"))
    app.window.set_titlebar(header)
    provider = Gtk.CssProvider()
    provider.load_from_path(str(Path(__file__).with_name("desktop.css")))
    Gtk.StyleContext.add_provider_for_display(Gdk.Display.get_default(), provider, Gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)

    root = box(0, True)
    root.add_css_class("workspace")
    app.window.set_child(root)
    sidebar = box(7, style="sidebar")
    app.sidebar = sidebar
    sidebar.set_size_request(70, -1)
    root.append(sidebar)
    brand = box(9, True)
    brand.set_margin_top(22)
    brand.set_margin_bottom(22)
    brand.set_margin_start(18)
    brand.set_margin_end(18)
    art = brand_image()
    image = Gtk.Image.new_from_pixbuf(GdkPixbuf.Pixbuf.new_from_file_at_scale(str(art), 32, 32, True))
    image.update_property([Gtk.AccessibleProperty.LABEL], ["Логотип Quantway"])
    brand.append(image)
    app.brand_wordmark = label("Quantway", "brand")
    app.brand_wordmark.set_visible(False)
    brand.append(app.brand_wordmark)
    sidebar.append(brand)
    app.stack = Gtk.Stack()
    app.stack.set_hexpand(True)
    app.stack.set_vexpand(True)
    app.stack.set_hhomogeneous(False)
    app.stack.set_vhomogeneous(False)
    # Native instant transitions respect reduced-motion preferences as well.
    app.stack.set_transition_type(Gtk.StackTransitionType.NONE)
    app.nav_buttons = []
    app.nav_labels = []
    app.nav_by_page = {}
    # Keep legacy indices stable; Tinc is inserted visually below Home.
    for page, title, icon in (("home", "Главная", "network-vpn-symbolic"), ("routes", "Маршруты", "view-list-symbolic"), ("account", "Кабинет", "avatar-default-symbolic"), ("external", "Другие VPN", "network-server-symbolic"), ("tinc", "Tinc", "network-workgroup-symbolic")):
        nav = Gtk.ToggleButton()
        nav.set_name(page)
        nav.add_css_class("nav-button")
        nav.set_margin_start(10)
        nav.set_margin_end(10)
        row = box(10, True)
        row.append(Gtk.Image.new_from_icon_name(icon))
        nav_label = label(title)
        nav_label.set_visible(False)
        row.append(nav_label)
        app.nav_labels.append(nav_label)
        nav.set_tooltip_text(title)
        nav.set_child(row)
        nav.update_property([Gtk.AccessibleProperty.LABEL], [title])
        if app.nav_buttons:
            nav.set_group(app.nav_buttons[0])
        nav.connect("toggled", app.navigate, page)
        if page == "tinc":
            nav.connect("clicked", app.open_tinc_page)
            sidebar.insert_child_after(nav, app.nav_by_page["home"])
        else:
            nav.connect("clicked", lambda _b, destination=page: app.stack.set_visible_child_name(destination))
            sidebar.append(nav)
        app.nav_buttons.append(nav)
        app.nav_by_page[page] = nav
    spacer = box()
    spacer.set_vexpand(True)
    sidebar.append(spacer)
    app.sidebar_state = label("VPN отключён", "caption", wrap=True)
    app.sidebar_state.set_visible(False)
    app.sidebar_state.set_margin_start(18)
    app.sidebar_state.set_margin_end(12)
    sidebar.append(app.sidebar_state)
    foot = label("Quantway для " + platform_label(), "caption")
    app.sidebar_foot = foot
    foot.set_visible(False)
    foot.set_margin_start(18)
    foot.set_margin_bottom(20)
    sidebar.append(foot)

    app.workspace_root = root
    content = box(12)
    app.content_area = content
    content.set_margin_top(20)
    content.set_margin_bottom(18)
    content.set_margin_start(24)
    content.set_margin_end(24)
    content.set_hexpand(True)
    root.append(content)
    app.notice = box(9, True, "notice")
    app.notice_icon = Gtk.Image.new_from_icon_name("dialog-information-symbolic")
    app.notice_icon.set_valign(Gtk.Align.CENTER)
    app.notice.append(app.notice_icon)
    app.status = StatusMessage(app.update_notice)
    app.notice_info = status_label("", wrap=True)
    app.notice_error = status_label("", wrap=True, urgent=True)
    app.notice_text = app.notice_info
    for message in (app.notice_info, app.notice_error):
        message.set_hexpand(True)
        message.set_lines(2)
        message.set_ellipsize(Pango.EllipsizeMode.END)
        message.set_wrap_mode(Pango.WrapMode.WORD_CHAR)
        app.notice.append(message)
    app.stack.connect("notify::visible-child-name", app.update_notice)
    app.notice_details = icon_button("view-more-symbolic", "Прочитать сообщение целиком", app.show_notice_details)
    app.notice.append(app.notice_details)
    app.notice_dismiss = icon_button("window-close-symbolic", "Закрыть сообщение", lambda _b: app.status.set_text(""))
    app.notice.append(app.notice_dismiss)
    app.notice.update_property([Gtk.AccessibleProperty.LABEL], ["Подсказки и сообщения"])
    content.append(app.stack)
    content.append(app.notice)

    home = box(0)
    welcome = box(20)
    welcome.append(heading("Подключитесь к Quantway"))
    welcome.append(label("Войдите в аккаунт или добавьте подписку своего VPN-провайдера.", "muted", True))
    own = box(12, style="panel")
    own.append(heading("У меня есть аккаунт Quantway", 2))
    own.append(label("Подписка этого компьютера загрузится после входа.", "muted", True))
    own.append(button("Войти в Quantway", lambda _b: app.nav_buttons[2].set_active(True), "primary"))
    welcome.append(own)
    other = box(12, style="quiet-panel")
    other.append(heading("Подписка другого провайдера", 2))
    other.append(label("Добавьте выданную им ссылку. Вход в Quantway не потребуется.", "muted", True))
    other.append(button("Добавить подписку", lambda _b: app.nav_buttons[3].set_active(True)))
    welcome.append(other)
    app.welcome_panel = scrolled(welcome)
    home.append(app.welcome_panel)
    app.connection_panel = box(0)
    app.connection_panel.set_visible(False)
    home.append(app.connection_panel)
    app.connection_body = box(12)
    app.connection_scroll = scrolled(app.connection_body)
    app.connection_scroll.set_vexpand(False)
    app.connection_scroll.set_propagate_natural_height(True)
    app.connection_scroll.set_max_content_height(360)
    app.connection_panel.append(app.connection_scroll)
    home_heading = box(12)
    app.home_heading = home_heading
    home_heading_words = box(4)
    home_heading_words.set_hexpand(True)
    title = heading("Главная")
    title.set_hexpand(True)
    home_heading_words.append(title)
    app.source_name = label("", "caption")
    app.source_name.set_visible(False)
    app.source_name.set_ellipsize(Pango.EllipsizeMode.END)
    app.source_name.connect("notify::label", lambda item, _p: item.set_tooltip_text(item.get_text()))
    home_heading_words.append(app.source_name)
    home_heading.append(home_heading_words)
    app.home_state = label("Не подключён", "state-pill")
    app.home_state.set_valign(Gtk.Align.CENTER)
    home_heading.append(app.home_state)
    app.connection_body.append(home_heading)
    source_row = box(12, True, "source-context")
    source_words = box(8, True)
    source_words.set_valign(Gtk.Align.CENTER)
    source_words.set_hexpand(True)
    source_words.append(label("Подписка", "caption"))
    app.home_source_name = label("", "row-title", True)
    source_words.append(app.home_source_name)
    source_row.append(source_words)
    app.source_switch_button = Gtk.MenuButton(label="Сменить")
    app.source_switch_button.update_property([Gtk.AccessibleProperty.LABEL], ["Сменить VPN-подписку"])
    app.source_popover = Gtk.Popover()
    app.source_choices = box(6)
    app.source_choices.add_css_class("workspace")
    app.source_popover.set_child(app.source_choices)
    app.source_switch_button.set_popover(app.source_popover)
    source_row.append(app.source_switch_button)
    app.connection_body.append(source_row)
    connection = box(12, style="panel")
    connection.add_css_class("connection-card")
    row = box(12, True)
    symbol = Gtk.Image.new_from_icon_name("network-vpn-symbolic")
    symbol.add_css_class("connection-symbol")
    symbol.set_pixel_size(18)
    symbol.set_valign(Gtk.Align.START)
    row.append(symbol)
    words = box(4)
    words.set_hexpand(True)
    app.connection_title = status_label("Выберите VPN-подписку", "connection-title")
    words.append(app.connection_title)
    app.connection_description = label("Загрузите подписку, чтобы подобрать рабочий маршрут.", "muted", True)
    words.append(app.connection_description)
    row.append(words)
    connection.append(row)
    app.add_subscription_button = button("Войти в Quantway", lambda _b: app.nav_buttons[2].set_active(True), "primary")
    app.add_subscription_button.add_css_class("connection-action")
    app.connect_button = button("Подключить", app.toggle_vpn, "primary")
    app.connect_button.add_css_class("connection-action")
    app.connect_button.set_halign(Gtk.Align.START)
    app.connect_button.set_size_request(164, -1)
    app.connect_button.set_sensitive(False)
    app.connect_button.set_visible(False)
    app.probe_button = button("Проверить маршруты", app.start_probe)
    app.probe_button.set_sensitive(False)
    connection.append(app.build_tinc_home_controls())
    summary = box(7)
    app.ordinary_route_summary = summary
    summary.set_hexpand(True)
    app.summary_title = label("Маршрут", "caption")
    summary.append(app.summary_title)
    app.choose_button = button("", lambda _b: app.nav_buttons[1].set_active(True), "route-choice")
    app.choose_button.set_halign(Gtk.Align.FILL)
    app.choose_button.set_hexpand(True)
    route_content = box(12, True)
    route_words = box(4)
    route_words.set_hexpand(True)
    app.route_name = label("Маршрут не выбран", "route-name", True)
    route_words.append(app.route_name)
    app.route_detail = label("—", "metric-value")
    app.route_protocol = label("—", "caption", True)
    route_words.append(app.route_protocol)
    route_content.append(route_words)
    route_content.append(app.route_detail)
    route_content.append(Gtk.Image.new_from_icon_name("go-next-symbolic"))
    app.choose_button.set_child(route_content)
    summary.append(app.choose_button)
    app.route_choice = label("После проверки", "muted", True)
    summary.append(app.route_choice)
    connection.append(summary)
    app.connection_settings_actions = action_row(app.probe_button, app.tinc_mesh_button)
    connection.append(app.connection_settings_actions)
    app.progress_box, app.progress_spinner, app.progress_text, app.progress_percent = progress_widgets()
    connection.append(app.progress_box)
    app.connection_body.append(connection)

    app.home_columns = box(12)
    app.best_box = box(10)
    app.best_box.set_hexpand(True)
    app.best_title = heading("Другие маршруты", 2)
    app.best_title.set_visible(False)
    app.best_title.set_wrap(True)
    app.best_box.append(app.best_title)
    app.best_list = box(7)
    app.best_box.append(app.best_list)
    app.best_note = label("Для смены маршрута отключите VPN.", "caption", True)
    app.best_box.append(app.best_note)
    app.best_placeholder = label("После проверки здесь появятся до трёх рабочих вариантов.", "muted", True)
    app.best_box.append(app.best_placeholder)
    app.best_expander = disclosure("Другие маршруты", app.best_box)
    app.home_columns.append(app.best_expander)
    app.connection_body.append(app.home_columns)
    app.connection_actions = box(12, True, "connection-footer")
    app.connection_actions.append(app.add_subscription_button)
    app.connection_actions.append(app.connect_button)
    app.connection_hint = label("", "caption", True)
    app.connection_hint.set_valign(Gtk.Align.CENTER)
    app.connection_hint.set_hexpand(True)
    app.connection_hint.set_lines(2)
    app.connection_hint.set_ellipsize(Pango.EllipsizeMode.END)
    app.connection_actions.prepend(app.connection_hint)
    app.connection_panel.append(app.connection_actions)
    app.stack.add_named(home, "home")

    routes = box(12)
    route_header = box(10, True)
    app.routes_header = route_header
    route_heading = box(4)
    route_heading.set_hexpand(True)
    route_heading.append(heading("Маршруты"))
    app.heading = label("Проверка ещё не выполнялась", "caption")
    route_heading.append(app.heading)
    route_header.append(route_heading)
    app.routes_probe_button = button("Проверить маршруты", app.start_probe)
    app.routes_probe_button.set_sensitive(False)
    route_header.append(app.routes_probe_button)
    routes.append(route_header)
    app.routes_progress_box, app.routes_spinner, app.routes_progress_text, app.routes_progress_percent = progress_widgets()
    routes.append(app.routes_progress_box)
    app.probe_button.connect("notify::label", app.sync_probe_button)
    app.probe_button.connect("notify::sensitive", app.sync_probe_button)
    app.routes_lock_hint = label("Проверенный маршрут можно переключить с подтверждением. Для новой проверки отключите VPN.", "caption", True)
    app.routes_lock_hint.set_visible(False)
    routes.append(app.routes_lock_hint)
    app.route_search = Gtk.SearchEntry(placeholder_text="Название маршрута или технология")
    app.route_search.set_hexpand(True)
    app.route_search.update_property([Gtk.AccessibleProperty.LABEL], ["Поиск маршрутов по названию или технологии"])
    app.route_search.connect("search-changed", app.filter_routes)
    app.route_filters = box(10, True)
    app.route_filters.append(app.route_search)
    app.route_available = Gtk.CheckButton(label="Только доступные")
    app.route_available.connect("toggled", app.filter_routes)
    app.route_filters.append(app.route_available)
    routes.append(app.route_filters)
    app.routes_empty = label("Войдите в Quantway или подключите внешнюю подписку. Маршруты появятся после загрузки.", "muted", True)
    routes.append(app.routes_empty)
    app.listbox = Gtk.ListBox()
    app.listbox.add_css_class("route-list")
    app.listbox.set_selection_mode(Gtk.SelectionMode.SINGLE)
    app.listbox.connect("row-selected", app.select_route)
    routes.append(scrolled(app.listbox))
    app.route_preview_box = box(12, True, "route-preview")
    app.route_preview_words = box(4)
    app.route_preview_words.set_hexpand(True)
    app.route_preview_title = heading("Выбранный маршрут", 2)
    app.route_preview_detail = label("", "caption", True)
    app.route_preview_words.append(app.route_preview_title)
    app.route_preview_words.append(app.route_preview_detail)
    app.route_preview_box.append(app.route_preview_words)
    app.route_apply_button = button("На Главную", app.apply_route_preview, "primary")
    app.route_preview_box.append(app.route_apply_button)
    app.route_preview_box.set_visible(False)
    routes.append(app.route_preview_box)
    app.stack.add_named(routes, "routes")
    app.stack.add_named(app.build_external_page(), "external")
    app.stack.add_named(scrolled(app.build_tinc_page()), "tinc")

    account = box(16)
    account_header = box(12, True)
    account_title = heading("Кабинет")
    account_title.set_hexpand(True)
    account_header.append(account_title)
    app.account_refresh_button = icon_button("view-refresh-symbolic", "Обновить кабинет", app.refresh_cabinet)
    app.account_refresh_button.set_visible(False)
    account_header.append(app.account_refresh_button)
    account.append(account_header)
    app.account_description = label("Подписка, устройства и сервисы вашего аккаунта Quantway.", "muted", True)
    account.append(app.account_description)
    login_panel = box(14, style="panel")
    app.login_heading = label("Войти в Quantway", "section-title")
    login_panel.append(app.login_heading)
    app.login_hint = label("Используйте код из кабинета или Telegram-бота.", "muted", True)
    login_panel.append(app.login_hint)
    app.account_row = box(12)
    app.code = Gtk.Entry()
    app.code.set_visibility(False)
    app.code.set_placeholder_text("Введите код входа")
    app.code.connect("activate", app.login)
    app.account_row.append(field("_Код входа", app.code))
    app.login_button = button("Войти", app.login, "primary")
    app.account_row.append(app.login_button)
    login_panel.append(app.account_row)
    app.account_summary = box(9)
    app.account_summary.add_css_class("subscription-summary")
    app.account_summary.set_visible(False)
    app.account_headline = label("Подписка Quantway", "section-title", True)
    app.account_days = label("", "connection-title")
    app.account_until = label("", "muted", True)
    app.account_state = label("", "status-pill")
    summary_heading = box(12, True)
    app.account_headline.set_hexpand(True)
    summary_heading.append(app.account_headline)
    summary_heading.append(app.account_state)
    app.account_summary.append(summary_heading)
    app.account_card_columns = box(24)
    subscription_details = box(9)
    subscription_details.set_hexpand(True)
    for child in (app.account_days, app.account_until):
        subscription_details.append(child)
    app.account_actions = box(10)
    app.renew_button = button("Продлить подписку", app.load_plans, "primary")
    app.account_actions.append(app.renew_button)
    subscription_details.append(app.account_actions)
    app.account_card_columns.append(subscription_details)
    app.account_stats_panel = app.build_account_stats()
    app.account_card_columns.append(app.account_stats_panel)
    app.account_summary.append(app.account_card_columns)
    app.account_links = box(8)
    app.account_links.set_visible(False)
    login_panel.append(app.account_summary)
    app.devices = box(8)
    app.device_scroll = scrolled(app.devices)
    app.device_scroll.set_vexpand(False)
    app.device_scroll.set_min_content_height(180)
    app.device_scroll.set_max_content_height(260)
    app.device_scroll.set_propagate_natural_height(True)
    app.device_scroll.set_visible(False)
    login_panel.append(app.device_scroll)
    app.session_hint = label("Вход сохраняется в системном хранилище паролей " + platform_label() + ".", "caption", True)
    login_panel.append(app.session_hint)
    app.session_retry = button("Восстановить вход", app.retry_saved_account)
    app.session_retry.set_halign(Gtk.Align.START)
    app.session_retry.set_visible(False)
    login_panel.append(app.session_retry)
    account.append(login_panel)
    account.append(app.account_links)
    app.stack.add_named(scrolled(account), "account")

    renewal = box(14)
    renewal_header = box(12, True)
    renewal_header.append(icon_button("go-previous-symbolic", "Назад в кабинет", lambda _b: app.stack.set_visible_child_name("account")))
    renewal_title = heading("Продление подписки")
    renewal_title.set_hexpand(True)
    renewal_header.append(renewal_title)
    renewal.append(renewal_header)
    renewal.append(label("Выберите тариф и способ оплаты. Оплата откроется у платёжного провайдера.", "muted", True))
    app.plans_retry = button("Загрузить тарифы ещё раз", app.load_plans)
    app.plans_retry.set_visible(False)
    renewal.append(app.plans_retry)
    app.plans_box = box(14)
    app.plans_box.set_visible(False)
    renewal.append(app.plans_box)
    app.stack.add_named(scrolled(renewal), "renew")
    app.nav_buttons[0].set_active(True)
    app.window.add_tick_callback(app.update_responsive_layout)
    app.window.present()
