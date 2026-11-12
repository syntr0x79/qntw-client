"""Quantway's cookie in the desktop Secret Service; no plaintext credential file.

The optional local marker contains no secrets. It prevents auto-login after an
explicit logout even when a locked/unavailable keyring cannot delete its item.
"""
import os
from pathlib import Path
import time
import stat
import tempfile

from platform_runtime import is_windows

from gi.repository import Gio, GLib


class SessionStoreError(Exception):
    pass


class SessionStoreCancelled(SessionStoreError):
    pass


def valid_session_cookie(value):
    return (isinstance(value, str) and value.startswith("qw_session=") and 11 < len(value) <= 4096 and
            all(33 <= ord(char) <= 126 and char not in ';,\\"' for char in value[11:]))


class RestorePolicy:
    def __init__(self, directory=None):
        self.directory = Path(directory) if directory is not None else Path(GLib.get_user_config_dir()) / "quantway"
        self.path = self.directory / "session-autologin-disabled"

    def disabled(self):
        try:
            self.path.lstat()
            return True
        except FileNotFoundError:
            return False
        except OSError:
            return True

    def disable(self):
        self.directory.mkdir(mode=0o700, parents=True, exist_ok=True)
        if is_windows():
            # Windows does not support directory fsync/fchmod. An atomic empty
            # marker inherits the current user's AppData ACL and never follows
            # an existing marker link when replacing it.
            try:
                info = self.path.lstat()
                if stat.S_ISLNK(info.st_mode) or getattr(info, "st_file_attributes", 0) & 0x400:
                    raise OSError("Недопустимый файл настроек входа.")
            except FileNotFoundError:
                pass
            descriptor, temporary = tempfile.mkstemp(prefix=".session-autologin-", dir=self.directory)
            try:
                with os.fdopen(descriptor, "wb") as stream:
                    stream.flush()
                    os.fsync(stream.fileno())
                os.replace(temporary, self.path)
            finally:
                Path(temporary).unlink(missing_ok=True)
            return
        flags = os.O_WRONLY | os.O_CREAT | os.O_TRUNC | getattr(os, "O_NOFOLLOW", 0)
        descriptor = os.open(self.path, flags, 0o600)
        try:
            os.fchmod(descriptor, 0o600)
            os.fsync(descriptor)
        finally:
            os.close(descriptor)
        directory_fd = os.open(self.directory, os.O_RDONLY | getattr(os, "O_DIRECTORY", 0))
        try:
            os.fsync(directory_fd)
        finally:
            os.close(directory_fd)

    def enable(self):
        self.path.unlink(missing_ok=True)


class SecretSessionStore:
    NAME = "org.freedesktop.secrets"
    ROOT = "/org/freedesktop/secrets"
    SERVICE = "org.freedesktop.Secret.Service"
    ITEM = "org.freedesktop.Secret.Item"
    COLLECTION = "org.freedesktop.Secret.Collection"
    ATTRIBUTES = {"application": "link.quantway.desktop", "purpose": "account-session", "version": "1"}

    def __init__(self, connection=None, prompt_timeout=60):
        self.connection = connection
        self.prompt_timeout = prompt_timeout

    def _bus(self):
        if self.connection is None:
            try:
                self.connection = Gio.bus_get_sync(Gio.BusType.SESSION, None)
            except GLib.Error:
                raise SessionStoreError("Хранилище паролей Linux недоступно.") from None
        return self.connection

    def _call(self, path, interface, method, signature=None, values=(), cancel=None):
        if cancel is not None and cancel.is_cancelled():
            raise SessionStoreCancelled("Действие с хранилищем отменено.")
        try:
            parameters = GLib.Variant(signature, values) if signature else None
            return self._bus().call_sync(self.NAME, path, interface, method, parameters, None,
                                         Gio.DBusCallFlags.NONE, 5000, cancel).unpack()
        except GLib.Error:
            if cancel is not None and cancel.is_cancelled():
                raise SessionStoreCancelled("Действие с хранилищем отменено.") from None
            raise SessionStoreError("Не удалось обратиться к хранилищу паролей Linux.") from None

    def _prompt(self, path, cancel=None):
        if path == "/":
            return None
        context = GLib.MainContext.new()
        context.push_thread_default()
        result = []
        subscription = None
        def completed(_bus, _sender, _path, _interface, _signal, parameters):
            result.append(parameters.unpack())
        try:
            subscription = self._bus().signal_subscribe(self.NAME, "org.freedesktop.Secret.Prompt", "Completed", path,
                                                         None, Gio.DBusSignalFlags.NONE, completed)
            self._call(path, "org.freedesktop.Secret.Prompt", "Prompt", "(s)", ("",), cancel)
            deadline = time.monotonic() + self.prompt_timeout
            while not result:
                if (cancel is not None and cancel.is_cancelled()) or time.monotonic() >= deadline:
                    try:
                        self._call(path, "org.freedesktop.Secret.Prompt", "Dismiss")
                    except SessionStoreError:
                        pass
                    raise SessionStoreCancelled("Разблокировка хранилища отменена.")
                while context.pending():
                    context.iteration(False)
                if not result:
                    time.sleep(0.03)
            dismissed, value = result[0]
            if dismissed:
                raise SessionStoreCancelled("Разблокировка хранилища отменена.")
            return value.unpack() if isinstance(value, GLib.Variant) else value
        except GLib.Error:
            raise SessionStoreError("Не удалось разблокировать хранилище паролей Linux.") from None
        finally:
            if subscription is not None:
                self._bus().signal_unsubscribe(subscription)
            context.pop_thread_default()

    def _items(self, cancel):
        unlocked, locked = self._call(self.ROOT, self.SERVICE, "SearchItems", "(a{ss})", (self.ATTRIBUTES,), cancel)
        return list(dict.fromkeys(unlocked + locked))

    def _unlock(self, objects, cancel):
        if objects:
            _unlocked, prompt = self._call(self.ROOT, self.SERVICE, "Unlock", "(ao)", (objects,), cancel)
            self._prompt(prompt, cancel)

    def _open(self, cancel):
        _output, session = self._call(self.ROOT, self.SERVICE, "OpenSession", "(sv)", ("plain", GLib.Variant("s", "")), cancel)
        if session == "/":
            raise SessionStoreError("Не удалось открыть хранилище паролей Linux.")
        return session

    def _close(self, session):
        try:
            self._call(session, "org.freedesktop.Secret.Session", "Close")
        except SessionStoreError:
            pass

    def load(self, cancel=None):
        items = self._items(cancel)
        if not items:
            return None
        if len(items) != 1:
            raise SessionStoreError("В хранилище несколько записей Quantway. Войдите заново, чтобы обновить сохранённый вход.")
        self._unlock(items, cancel)
        session = self._open(cancel)
        try:
            secret, = self._call(items[0], self.ITEM, "GetSecret", "(o)", (session,), cancel)
            if secret[0] != session or secret[1]:
                raise SessionStoreError("Не удалось прочитать сохранённый вход.")
            try:
                cookie = bytes(secret[2]).decode("utf-8")
            except (ValueError, UnicodeError):
                raise SessionStoreError("Не удалось прочитать сохранённый вход.") from None
            if not valid_session_cookie(cookie):
                raise SessionStoreError("Сохранённый вход повреждён. Войдите заново.")
            return cookie
        finally:
            self._close(session)

    def save(self, cookie, cancel=None):
        if not valid_session_cookie(cookie):
            raise SessionStoreError("Невозможно сохранить некорректную сессию.")
        items = self._items(cancel)
        collection = None
        if items:
            self._unlock(items, cancel)
        else:
            collection, = self._call(self.ROOT, self.SERVICE, "ReadAlias", "(s)", ("default",), cancel)
            if collection == "/":
                properties = {"org.freedesktop.Secret.Collection.Label": GLib.Variant("s", "Quantway")}
                collection, prompt = self._call(self.ROOT, self.SERVICE, "CreateCollection", "(a{sv}s)", (properties, "default"), cancel)
                prompted = self._prompt(prompt, cancel)
                if collection == "/":
                    collection = prompted
                if not collection or collection == "/":
                    raise SessionStoreCancelled("Создание хранилища отменено.")
            self._unlock([collection], cancel)
        session = self._open(cancel)
        secret = (session, [], list(cookie.encode("utf-8")), "text/plain; charset=utf-8")
        try:
            if items:
                self._call(items[0], self.ITEM, "SetSecret", "((oayays))", (secret,), cancel)
                for duplicate in items[1:]:
                    prompt, = self._call(duplicate, self.ITEM, "Delete", cancel=cancel)
                    self._prompt(prompt, cancel)
            else:
                properties = {"org.freedesktop.Secret.Item.Label": GLib.Variant("s", "Quantway — вход в аккаунт"),
                              "org.freedesktop.Secret.Item.Attributes": GLib.Variant("a{ss}", self.ATTRIBUTES)}
                item, prompt = self._call(collection, self.COLLECTION, "CreateItem", "(a{sv}(oayays)b)", (properties, secret, True), cancel)
                prompted = self._prompt(prompt, cancel)
                if item == "/" and not prompted:
                    raise SessionStoreCancelled("Сохранение входа отменено.")
        finally:
            self._close(session)

    def delete(self, cancel=None):
        items = self._items(cancel)
        self._unlock(items, cancel)
        for item in items:
            prompt, = self._call(item, self.ITEM, "Delete", cancel=cancel)
            self._prompt(prompt, cancel)


class WindowsSessionStore:
    """Adapt the same account lifecycle to the current Windows user's vault."""

    def __init__(self, vault=None):
        self.vault = vault

    def _cancelled(self, cancel):
        if cancel is not None and cancel.is_cancelled():
            raise SessionStoreCancelled("Действие с хранилищем отменено.")

    def _vault(self):
        if self.vault is None:
            try:
                from credential_windows import WindowsCredentialVault
                self.vault = WindowsCredentialVault()
            except (OSError, AttributeError):
                raise SessionStoreError("Диспетчер учётных данных Windows недоступен.") from None
        return self.vault

    def load(self, cancel=None):
        self._cancelled(cancel)
        try:
            raw = self._vault().read()
            self._cancelled(cancel)
            if raw is None:
                return None
            cookie = raw.decode("utf-8")
            if not valid_session_cookie(cookie):
                raise SessionStoreError("Сохранённый вход повреждён. Войдите заново.")
            return cookie
        except (OSError, ValueError, UnicodeError):
            raise SessionStoreError("Не удалось прочитать сохранённый вход Windows.") from None

    def save(self, cookie, cancel=None):
        self._cancelled(cancel)
        if not valid_session_cookie(cookie):
            raise SessionStoreError("Невозможно сохранить некорректную сессию.")
        try:
            self._vault().write(cookie.encode("utf-8"))
            self._cancelled(cancel)
        except OSError:
            raise SessionStoreError("Не удалось сохранить вход в Windows.") from None

    def delete(self, cancel=None):
        self._cancelled(cancel)
        try:
            self._vault().delete()
            self._cancelled(cancel)
        except OSError:
            raise SessionStoreError("Не удалось удалить сохранённый вход Windows.") from None


def create_session_store():
    return WindowsSessionStore() if is_windows() else SecretSessionStore()
