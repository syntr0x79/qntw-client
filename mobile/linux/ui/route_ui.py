"""Route presentation and explicit selection; transport ownership stays in Quantway."""
import gi
gi.require_version("Gtk", "4.0")
from gi.repository import Gtk, Pango

from desktop_ui import box, label


def route_title(profile):
    return str(profile.get("label") or "Маршрут") if profile is not None else "Маршрут не выбран"


def route_technology(profile):
    profile = profile or {}
    return " / ".join(str(value).upper() for value in
                      (profile.get("protocol") or profile.get("engine"), profile.get("transport")) if value) or "—"


def route_state(profile):
    outcome = (profile or {}).get("outcome")
    return {"ok": "Доступен", "fail": "Недоступен", "cancelled": "Проверка остановлена",
            "unprobeable": "Не удалось проверить"}.get(outcome, "Не проверен")


def route_measurement(profile):
    outcome = (profile or {}).get("outcome")
    if outcome == "fail":
        return "Не прошёл проверку"
    if outcome == "cancelled":
        return "Проверка остановлена"
    if outcome == "unprobeable":
        return "Не удалось проверить"
    if outcome != "ok":
        return "Не проверен"
    return "Проверка: " + str(max(0, int(profile.get("elapsed_ms") or 0))) + " мс"


def route_locked(app):
    active = app.vpn_process is not None or app.connected_since is not None
    return bool(app.closing or app.vpn_starting or app.probe_running or app.loading_profiles or
                app.results_stale or app.switching_route or getattr(app, "_device_mutating", False) or
                getattr(app, "tinc_worker_active", False) or getattr(app, "connection_mode", "ordinary") == "tinc" or
                (getattr(app, "_vpn_worker_generation", None) is not None and app.vpn_process is None))


def current_profile(app, profile):
    job_id = (profile or {}).get("job_id")
    if not job_id:
        return None
    return next((item for item in app.current_profiles if item.get("job_id") == job_id and
                 item.get("outcome") == "ok" and item.get("job")), None)


def remember_preview(app, profile):
    app._route_preview_id = (profile or {}).get("job_id")
    app._route_preview_link = app.loaded_link


def preview_profile(app):
    if getattr(app, "_route_preview_link", app.loaded_link) != app.loaded_link:
        return None
    wanted = getattr(app, "_route_preview_id", (app.selected_profile or {}).get("job_id"))
    return next((item for item in app.current_profiles if item.get("job_id") == wanted), None)


def refresh_preview(app):
    if not hasattr(app, "route_preview_box"):
        return
    profile = preview_profile(app)
    app.route_preview_box.set_visible(bool(app.current_profiles))
    app.route_preview_title.set_text(route_title(profile) if profile else "Выберите маршрут в списке")
    details = " · ".join((route_state(profile), route_measurement(profile), route_technology(profile))) if profile else "Поиск учитывает название и технологию подключения."
    app.route_preview_detail.set_text(details)
    if getattr(app, "tinc_worker_active", False) or getattr(app, "connection_mode", "ordinary") == "tinc":
        app.route_preview_title.set_text("Выбран Tinc")
        app.route_preview_detail.set_text("Сейчас выбран Tinc. Сменить способ подключения можно на Главной.")
        app.route_apply_button.set_label("На Главную")
        app.route_apply_button.set_sensitive(not app.closing)
        return
    active = app.vpn_process is not None or app.connected_since is not None
    same = bool(profile and profile.get("job_id") == (app.selected_profile or {}).get("job_id"))
    app.route_apply_button.set_label("Текущий маршрут" if active and same else "Переключить маршрут" if active else "На Главную")
    app.route_apply_button.set_sensitive(bool(current_profile(app, profile)) and not route_locked(app) and not (active and same))


def update_route_card(app):
    profile = app.selected_profile
    name = route_title(profile)
    app.route_name.set_text(name)
    app.route_name.set_tooltip_text(name)
    app.route_detail.set_text(route_measurement(profile) if profile else "—")
    app.route_protocol.set_text(route_technology(profile))
    app.route_choice.set_text(("Выбран автоматически" if app.selection_automatic else "Выбран вручную") if profile else "Проверка подберёт доступный маршрут")
    if app.results_stale and profile:
        app.route_choice.set_text("Результат устарел. Нужна новая проверка")
    app.update_best_routes()


def update_best_routes(app):
    if not hasattr(app, "best_list"):
        return
    while child := app.best_list.get_first_child():
        app.best_list.remove(child)
    selected_id = (app.selected_profile or {}).get("job_id")
    working = sorted((p for p in app.current_profiles if p.get("outcome") == "ok" and p.get("job") and
                      p.get("job_id") != selected_id), key=lambda p: p.get("elapsed_ms", 0))
    alternatives = working[:3]
    for profile in alternatives:
        row = box(10, True)
        words = box(3)
        words.set_hexpand(True)
        name = label(route_title(profile))
        name.set_ellipsize(Pango.EllipsizeMode.END)
        words.append(name)
        words.append(label(route_technology(profile), "caption", True))
        row.append(words)
        row.append(label(route_measurement(profile), "caption", True))
        item = Gtk.Button()
        item.add_css_class("alternative")
        item.set_child(row)
        item.set_hexpand(True)
        item.set_sensitive(not route_locked(app))
        item.set_tooltip_text("Посмотреть маршрут: " + route_title(profile))
        item.connect("clicked", app.choose_best, profile)
        app.best_list.append(item)
    app.best_box.set_visible(True)
    app.best_placeholder.set_visible(not alternatives)
    app.best_note.set_visible(bool(alternatives) and (app.vpn_process is not None or app.vpn_starting or app.results_stale))
    app.best_note.set_text("Дождитесь новой проверки, чтобы выбрать маршрут." if app.results_stale else
                           "Выбор откроет просмотр. Подключение изменится только после подтверждения.")
    refresh_preview(app)


def choose_best(app, profile):
    if route_locked(app):
        return
    profile = current_profile(app, profile)
    if profile is None:
        return
    if not matches_filter(app, profile) and hasattr(app, "route_search"):
        app.route_search.set_text("")
        filter_routes(app)
    remember_preview(app, profile)
    if app.vpn_process is not None or app.connected_since is not None:
        for row in app.listbox_rows():
            if row.profile.get("job_id") == profile.get("job_id"):
                app.listbox.select_row(row)
                break
        app.nav_by_page["routes"].set_active(True)
        app.stack.set_visible_child_name("routes")
        refresh_preview(app)
        return
    app.selection_automatic = False
    app.selected_profile = profile
    app.connection_description.set_text("Маршрут выбран. Нажмите «Подключить», когда будете готовы.")
    app.update_route_card()
    for row in app.listbox_rows():
        if row.profile.get("job_id") == profile.get("job_id"):
            app.listbox.select_row(row)
            break
    app.mark_selected_route()
    app.sync_controls()
    app.status.set_text("Выбран маршрут: " + route_title(profile))


def select_route(app, row):
    if app.restore_route_selection or row is None or route_locked(app):
        return
    profile = current_profile(app, getattr(row, "profile", {}))
    if profile is None:
        return
    remember_preview(app, profile)
    if app.vpn_process is not None or app.connected_since is not None:
        refresh_preview(app)
        return
    app.selected_profile = profile
    if app.listbox.get_selected_row() is row and not getattr(app, "selecting_automatically", False):
        app.selection_automatic = False
        app.connection_description.set_text("Маршрут выбран. Нажмите «Подключить», когда будете готовы.")
    app.update_route_card()
    app.sync_controls()
    app.mark_selected_route()


def restore_selected_row(app):
    app.restore_route_selection = True
    try:
        remember_preview(app, app.selected_profile)
        for row in app.listbox_rows():
            if row.profile.get("job_id") == (app.selected_profile or {}).get("job_id"):
                app.listbox.select_row(row)
                break
    finally:
        app.restore_route_selection = False
    refresh_preview(app)


def mark_selected_route(app):
    selected_id = (app.selected_profile or {}).get("job_id")
    connected = app.connected_since is not None and not getattr(app, "tinc_worker_active", False)
    for row in app.listbox_rows():
        if hasattr(row, "selected_mark"):
            row.selected_mark.set_text("Подключён" if connected else "Выбран")
            row.selected_mark.set_visible(row.profile.get("job_id") == selected_id)
    refresh_preview(app)


def apply_route_preview(app):
    if app.closing:
        return
    if getattr(app, "tinc_worker_active", False) or getattr(app, "connection_mode", "ordinary") == "tinc":
        # This action only navigates. Switching transport remains an explicit
        # choice on Home and retains the existing confirmation/cleanup path.
        app.nav_by_page["home"].set_active(True)
        app.stack.set_visible_child_name("home")
        return
    if route_locked(app):
        return
    profile = current_profile(app, preview_profile(app))
    if profile is None:
        return
    if app.vpn_process is not None or app.connected_since is not None:
        app.confirm_route_switch(profile)
        return
    # Choosing while idle never starts a helper or a tunnel.
    app.nav_by_page["home"].set_active(True)
    app.stack.set_visible_child_name("home")


def matches_filter(app, profile):
    search = getattr(app, "route_search", None)
    text = search.get_text().strip().casefold() if search is not None else ""
    available = getattr(app, "route_available", None)
    if available is not None and available.get_active() and not (profile.get("outcome") == "ok" and profile.get("job")):
        return False
    public = " ".join(str(profile.get(key) or "") for key in ("label", "protocol", "transport", "engine")).casefold()
    return not text or text in public


def filter_routes(app):
    if app.closing or not hasattr(app, "listbox"):
        return False
    app.listbox.set_filter_func(lambda row: matches_filter(app, row.profile))
    preview = preview_profile(app)
    if preview is not None and not matches_filter(app, preview):
        remember_preview(app, None)
        app.listbox.unselect_all()
    visible = sum(matches_filter(app, row.profile) for row in app.listbox_rows())
    app.routes_empty.set_visible(not visible)
    app.routes_empty.set_text("Ничего не найдено. Измените поиск или отключите фильтр доступности." if app.current_profiles else
                              "Маршрутов пока нет. Выберите подписку на Главной.")
    refresh_preview(app)
    return False


def build_route_row(app, profile):
    content = box(5)
    for side, amount in (("top", 8), ("bottom", 8), ("start", 12), ("end", 12)):
        getattr(content, "set_margin_" + side)(amount)
    heading = box(12, True)
    name = label(route_title(profile), "route-label")
    name.set_hexpand(True)
    name.set_ellipsize(Pango.EllipsizeMode.END)
    name.set_tooltip_text(route_title(profile))
    heading.append(name)
    state = label(route_state(profile), "route-state")
    if profile.get("outcome") == "ok":
        state.add_css_class("success")
    heading.append(state)
    content.append(heading)
    details = box(12, True)
    technology = label(route_technology(profile), "route-technical", True)
    technology.set_hexpand(True)
    details.append(technology)
    details.append(label(route_measurement(profile), "caption", True))
    content.append(details)
    mark = label("Выбран", "selected-mark")
    mark.set_visible(profile.get("job_id") == (app.selected_profile or {}).get("job_id"))
    heading.append(mark)
    item = Gtk.ListBoxRow()
    item.add_css_class("route-row")
    item.profile, item.selected_mark = profile, mark
    item.set_child(content)
    item.set_selectable(profile.get("outcome") == "ok" and bool(profile.get("job")))
    return item


def show_result(app, profiles, error, loaded_link=None, generation=None):
    if not app.subscription_callback_current(generation):
        return False
    app.loading_profiles = False
    if error:
        if app.load_pending_account_subscription():
            return False
        app.status.set_text(error)
        app.sync_controls()
        return False
    if loaded_link is not None:
        app.loaded_link = loaded_link
        app.results_stale = True
        app.selected_profile = None
        remember_preview(app, None)
    app.current_profiles = profiles
    app.add_subscription_button.set_visible(not profiles)
    app.connect_button.set_visible(bool(profiles))
    while child := app.listbox.get_first_child():
        app.listbox.remove(child)
    for profile in profiles:
        app.listbox.append(build_route_row(app, profile))
    working = sum(p.get("outcome") == "ok" and bool(p.get("job")) for p in profiles)
    app.heading.set_text(f"{len(profiles)} маршрутов. Доступно: {working}" if profiles else "Пока нет маршрутов")
    filter_routes(app)
    app.update_route_card()
    app.sync_controls()
    if app.load_pending_account_subscription():
        return False
    if loaded_link is not None and profiles:
        app.nav_by_page["home"].set_active(True)
        app.start_probe(None)
    return False
