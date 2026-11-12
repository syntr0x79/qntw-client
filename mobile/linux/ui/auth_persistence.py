"""Restore only the Quantway cookie; account and device data remain server-owned."""
import subprocess
import threading

from gi.repository import Gio, GLib

from session_store import RestorePolicy, create_session_store, SessionStoreError
from platform_runtime import platform_label


class ServiceFailure(ValueError):
    def __init__(self, message, status=0, code=""):
        super().__init__(message)
        self.status = status
        self.code = code


class AuthPersistenceMixin:
    def init_auth_persistence(self):
        # Neither constructor contacts the bus nor reads a secret.
        self.session_store = create_session_store()
        self.restore_policy = RestorePolicy()
        self._auth_epoch = 0
        self._auth_intent_lock = threading.Lock()
        self._auth_io_lock = threading.Lock()
        self._auth_cancel = Gio.Cancellable()
        self._auth_started = False
        self._auth_operation = None
        self._auth_retry = None
        self._auth_cookie = None

    def auth_hint(self, text, retry=None):
        self._auth_retry = retry
        self.session_hint.set_text(text)
        self.session_retry.set_label("Сохранить вход" if retry == "save" else "Восстановить вход")
        self.session_retry.set_visible(retry is not None)

    def _auth_intent(self):
        with self._auth_intent_lock:
            self._auth_cancel.cancel()
            self._auth_epoch += 1
            self._auth_cancel = Gio.Cancellable()
            self._auth_operation = None
            return self._auth_epoch, self._auth_cancel

    def _auth_current(self, epoch):
        return epoch == self._auth_epoch

    def start_session_restore(self):
        if self._auth_started:
            return
        self._auth_started = True
        if self.restore_policy.disabled():
            return
        epoch, cancel = self._auth_intent()
        self._auth_operation = "load"
        self.auth_hint("Проверяем сохранённый вход…")

        def worker():
            cookie, error = None, False
            try:
                with self._auth_io_lock:
                    if self._auth_current(epoch):
                        cookie = self.session_store.load(cancel)
            except SessionStoreError:
                error = True
            GLib.idle_add(self._saved_cookie_loaded, epoch, cookie, error)
        threading.Thread(target=worker, daemon=True).start()

    def _saved_cookie_loaded(self, epoch, cookie, error):
        if self.closing or not self._auth_current(epoch):
            return False
        self._auth_operation = None
        if error:
            self.auth_hint("Хранилище входа недоступно. Разблокируйте его или войдите по коду.", "load")
        elif cookie:
            self._auth_cookie = cookie
            self._restore_account(epoch, cookie)
        else:
            self.auth_hint("Вход сохраняется в системном хранилище паролей " + platform_label() + ".")
        return False

    def _restore_account(self, epoch, cookie):
        self._auth_operation = "restore"
        source_context = (self.subscription_generation, self.active_source, self.stack.get_visible_child_name())
        self.auth_hint("Восстанавливаем вход в Quantway…")

        def worker():
            session, invalid = None, False
            try:
                session = self.call_helper("restore-session", {"cookie": cookie})["session"]
            except ServiceFailure as exc:
                # A 403 can also be CSRF protection or an account policy. It
                # does not establish that this saved credential has expired.
                invalid = exc.status == 401
            except (OSError, ValueError, KeyError, subprocess.TimeoutExpired):
                pass
            GLib.idle_add(self._account_restored, epoch, session, invalid, source_context)
        threading.Thread(target=worker, daemon=True).start()

    def _account_restored(self, epoch, session, invalid, source_context):
        if self.closing or not self._auth_current(epoch):
            return False
        self._auth_operation = None
        if invalid:
            self.forget_saved_account()
            self.auth_hint("Сохранённый вход больше не действует. Войдите по коду.")
        elif session:
            current = (self.subscription_generation, self.active_source, self.stack.get_visible_child_name())
            auto_import = current == source_context and current[1] is None and current[2] == "home"
            self.show_devices(session, "", self.account_generation, import_subscription=auto_import)
            self.auth_hint("Вход восстановлен. Он сохранится после закрытия приложения.")
        else:
            self.auth_hint("Не удалось восстановить кабинет. Сохранённый вход остался — попробуйте ещё раз.", "restore")
        return False

    def retry_saved_account(self, _button=None):
        if self.closing or self._auth_operation is not None:
            return
        if self._auth_retry == "save" and self.session:
            self.persist_account_session(self.session)
        elif self._auth_retry == "restore" and self._auth_cookie:
            self._restore_account(self._auth_epoch, self._auth_cookie)
        else:
            self._auth_started = False
            self.start_session_restore()

    def login_received(self, session, error, generation):
        if self.closing or generation != self.account_generation:
            return False
        if error:
            return self.show_devices(None, error, generation)
        # Keep login in progress until the vault acknowledges the save. Closing
        # the last window in this interval still lets the save complete.
        self.persist_account_session(session, generation=generation, login=True)
        return False

    def persist_account_session(self, session, generation=None, login=False):
        epoch, cancel = self._auth_intent()
        self._auth_operation = "save"
        cookie = session.get("cookie", "")
        self.auth_hint("Сохраняем вход в системном хранилище…")
        self.hold()

        def worker():
            error = False
            try:
                with self._auth_io_lock:
                    if self._auth_current(epoch):
                        self.session_store.save(cookie, cancel)
                        # Logout can happen during a keyring prompt. It must
                        # never be undone by that earlier save finishing late.
                        with self._auth_intent_lock:
                            if self._auth_current(epoch):
                                self.restore_policy.enable()
            except (SessionStoreError, OSError):
                error = True
            GLib.idle_add(self._account_saved, epoch, session, generation, login, error)
        threading.Thread(target=worker, daemon=True).start()

    def _account_saved(self, epoch, session, generation, login, error):
        self.release()
        if not self._auth_current(epoch):
            return False
        self._auth_operation = None
        if not error:
            self._auth_cookie = session.get("cookie")
        if self.closing:
            return False
        if login:
            self.show_devices(session, "", generation)
        if error:
            self.auth_hint("Вход открыт только на этот запуск: не удалось сохранить его в хранилище " + platform_label() + ".", "save")
            self.status.set_text("Вход открыт, но не сохранён. Разблокируйте хранилище паролей и нажмите «Сохранить вход» в кабинете.")
        else:
            self.auth_hint("Вход сохранён. Закрытие приложения не выходит из аккаунта.")
        return False

    def forget_saved_account(self):
        epoch, cancel = self._auth_intent()
        self._auth_operation = "delete"
        self._auth_cookie = None
        marker_error = False
        try:
            # No credential is written to this empty preference marker.
            self.restore_policy.disable()
        except OSError:
            marker_error = True
        self.auth_hint("Вход сохраняется в системном хранилище паролей " + platform_label() + ".")
        self.hold()

        def worker():
            error = False
            try:
                with self._auth_io_lock:
                    if self._auth_current(epoch):
                        self.session_store.delete(cancel)
            except SessionStoreError:
                error = True
            GLib.idle_add(self._account_forgotten, epoch, error and marker_error)
        threading.Thread(target=worker, daemon=True).start()

    def _account_forgotten(self, epoch, unsafe):
        self.release()
        if self._auth_current(epoch):
            self._auth_operation = None
            if not self.closing and unsafe:
                self.auth_hint("Не удалось отключить сохранённый вход: проверьте доступ к хранилищу и настройкам " + platform_label() + ".")
        return False

    def close_auth_persistence(self):
        # A confirmed login is saved even if the window closes immediately.
        # Loading/unlocking an old session can instead be safely cancelled.
        if self._auth_operation not in ("save", "delete"):
            self._auth_intent()
