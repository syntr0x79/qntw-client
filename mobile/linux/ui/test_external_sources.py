#!/usr/bin/env python3
"""Offline GTK regression for external sources and their private URL editor."""
from external_ui import ExternalMixin
from desktop_ui import StatusMessage
from gi.repository import Gtk, GLib

class App(ExternalMixin):
    def __init__(self):
        self.loading_profiles = self.probe_running = self.vpn_starting = False
        self.vpn_process = self.connected_since = None
        self.active_source = None
        self.status = StatusMessage(lambda: None)
        self.selected = []
        self.cleared = []
        self.window = Gtk.Window()
        self.init_external_state()
    def subscription_changed(self, *_): pass
    def sync_controls(self): self.external_sync_controls()
    def select_subscription(self, link, kind, source_id, name):
        self.selected.append((kind, source_id, name, link))
        self.active_source = dict(kind=kind, id=source_id, name=name, link=link)
        self.external_sync_controls()
    def clear_subscription_source(self, kind, source_id):
        self.cleared.append((kind, source_id))
        if self.active_source and self.active_source['id'] == source_id:
            self.active_source = None

def settle():
    context = GLib.MainContext.default()
    for _ in range(20):
        while context.pending(): context.iteration(False)

def descendants(widget):
    yield widget
    child = widget.get_first_child()
    while child:
        yield from descendants(child)
        child = child.get_next_sibling()

class Pop:
    def popdown(self): pass

app=App()
page=app.build_external_page()
app.window.set_child(page)
assert any(isinstance(widget,Gtk.Label) and widget.get_text()=="Внешние подписки" and
           widget.get_accessible_role()==Gtk.AccessibleRole.HEADING for widget in descendants(page))
app.window.present()
settle()
assert app.entry.get_parent() is None
app.entry.set_text('https://qntw.link/sub/our-private-link')
assert not app.external_url.get_text()
for bad in ('http://example.invalid/sub', 'https://user:pass@example.invalid/sub', 'https://example.invalid/sub#fragment', 'https://example.invalid:bad/sub', 'https://example.invalid/sub\nother'):
    assert not app.validate_external_link(bad), bad
app.external_name.set_text('Рабочий VPN')
app.external_url.set_text('https://provider.invalid/sub?token=fixture-secret')
app.add_external_subscription(None)
assert len(app.external_subscriptions)==1
assert app.selected[-1][0]=='external'
assert app._external_rows[0]['use'].get_label()=='Обновить маршруты'
assert not app.external_expander.get_expanded()
source=app.external_subscriptions[0]
assert app.external_url.get_text()==''
assert app.entry.get_text()=='https://qntw.link/sub/our-private-link'
for widget in descendants(app.external_list):
    if isinstance(widget, Gtk.Label):
        assert 'fixture-secret' not in widget.get_text()
        assert 'fixture-secret' not in (widget.get_tooltip_text() or '')
app.external_url.set_text(source['link'])
app.add_external_subscription(None)
assert len(app.external_subscriptions)==1
app.probe_running=True
app.external_sync_controls()
assert not app.load_button.get_sensitive()
assert not app._external_rows[0]['use'].get_sensitive()
before=len(app.selected)
app.use_external_subscription(None, source['id'])
assert len(app.selected)==before
app.probe_running=False
app._vpn_worker_generation=7
app.external_sync_controls()
assert not app._external_rows[0]['use'].get_sensitive()
app._vpn_worker_generation=None
app.external_sync_controls()
app.rename_external_subscription(Pop(),source['id'])
settle()
dialog=next(w for w in Gtk.Window.get_toplevels() if isinstance(w,Gtk.Dialog))
entry=next(w for w in descendants(dialog) if isinstance(w,Gtk.Entry))
entry.set_text('Личный VPN')
dialog.response(Gtk.ResponseType.ACCEPT)
assert source['name']=='Личный VPN'
assert app.active_source['name']=='Личный VPN'

# Cancelling or rejecting a new URL never changes the source or active route.
original=source['link']
before=len(app.selected)
app.edit_external_link(Pop(), source['id'])
dialog=next(w for w in Gtk.Window.get_toplevels() if isinstance(w,Gtk.Dialog))
entry=next(w for w in descendants(dialog) if isinstance(w,Gtk.Entry))
assert not entry.get_visibility()
entry.set_text('https://changed.invalid/sub?private=fixture')
dialog.response(Gtk.ResponseType.CANCEL)
assert source['link']==original and len(app.selected)==before
app.edit_external_link(Pop(), source['id'])
dialog=next(w for w in Gtk.Window.get_toplevels() if isinstance(w,Gtk.Dialog))
entry=next(w for w in descendants(dialog) if isinstance(w,Gtk.Entry))
entry.set_text('http://invalid.example/sub')
dialog.response(Gtk.ResponseType.ACCEPT)
assert source['link']==original and len(app.selected)==before
updated='https://changed.invalid/sub?private=fixture'
entry.set_text(updated)
dialog.response(Gtk.ResponseType.ACCEPT)
assert source['link']==updated
assert app.active_source['link']==updated
assert len(app.selected)==before+1

# Editing a different provider never activates it or replaces the current URL.
other={'id':'other-fixture','name':'Другой VPN','link':'https://other.invalid/sub'}
app.external_subscriptions.append(other)
app.render_external_subscriptions()
before=len(app.selected)
app.edit_external_link(Pop(), other['id'])
dialog=next(w for w in Gtk.Window.get_toplevels() if isinstance(w,Gtk.Dialog))
entry=next(w for w in descendants(dialog) if isinstance(w,Gtk.Entry))
entry.set_text('https://new-other.invalid/sub')
app.probe_running=True
dialog.response(Gtk.ResponseType.ACCEPT)
assert other['link']=='https://other.invalid/sub'
assert len(app.selected)==before
app.probe_running=False
dialog.response(Gtk.ResponseType.ACCEPT)
assert other['link']=='https://new-other.invalid/sub'
assert len(app.selected)==before
assert app.active_source['id']==source['id']
app.remove_external_subscription(Pop(),source['id'])
settle()
dialog=next(w for w in Gtk.Window.get_toplevels() if isinstance(w,Gtk.MessageDialog))
dialog.response(Gtk.ResponseType.ACCEPT)
assert app.external_subscriptions==[other]
assert app.cleared==[('external',source['id'])]
app.remove_external_subscription(Pop(),other['id'])
dialog=next(w for w in Gtk.Window.get_toplevels() if isinstance(w,Gtk.MessageDialog))
dialog.response(Gtk.ResponseType.ACCEPT)
assert not app.external_subscriptions
assert app.external_expander.get_expanded()
app.window.destroy()
print('External source smoke PASS; no network or helper used')
