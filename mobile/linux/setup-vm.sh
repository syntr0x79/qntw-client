#!/bin/sh
set -eu
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
case "${1:-}" in
  --check) mode=check ;;
  ''|--install) mode=install ;;
  *) echo 'Использование: setup-vm.sh [--check]' >&2; exit 2 ;;
esac
if [ "$mode" = install ] && [ "$(id -u)" != 0 ]; then
  "$here/setup-vm.sh" --check
  if ! /usr/bin/sudo -n true 2>/dev/null; then
    echo 'Для установки сетевых помощников потребуется пароль администратора Ubuntu.'
  fi
  exec /usr/bin/sudo -- /bin/sh "$here/setup-vm.sh" --install
fi
exec /usr/bin/python3 -I - "$here" "$mode" <<'PY'
import hashlib
import os
from pathlib import Path
import platform
import re
import shutil
import stat
import sys
import tempfile

here = Path(sys.argv[1])
mode = sys.argv[2]
bundle = here.parent
names = ("quantway-net-helper", "quantway-tinc-helper", "tinc", "tincd")

def fail(message):
    raise RuntimeError(message)

def source_path(relative):
    path = Path(os.path.abspath(here / relative))
    if not path.is_relative_to(bundle):
        fail("путь выходит за пределы архива")
    current = bundle
    for part in path.relative_to(bundle).parts:
        current = current / part
        if stat.S_ISLNK(os.lstat(current).st_mode):
            fail("символическая ссылка в архиве: " + relative)
    if not stat.S_ISREG(os.lstat(path).st_mode):
        fail("ожидался обычный файл: " + relative)
    return path

def read_source(relative, limit):
    path = source_path(relative)
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, "rb") as stream:
        if not stat.S_ISREG(os.fstat(stream.fileno()).st_mode):
            fail("ожидался обычный файл: " + relative)
        content = stream.read(limit + 1)
    if len(content) > limit:
        fail("слишком большой файл: " + relative)
    return content

def digest(content):
    return hashlib.sha256(content).hexdigest()

try:
    manifest = read_source("SHA256SUMS", 131072).decode("utf-8")
    expected = {}
    for line in manifest.splitlines():
        match = re.fullmatch(r"([0-9a-f]{64})  ([^\r\n\\]+)", line)
        if not match or match[2] in expected or Path(match[2]).is_absolute():
            fail("недопустимый SHA256SUMS")
        expected[match[2]] = match[1]
    required = {"setup-vm.sh", "run.sh", "README.md", "build/ARCH", "build/quantway-helper",
                "build/COPYING.tinc", "build/BUILD-INFO.tinc", "build/BUILD-INFO.client", "ui/quantway.py",
                "ui/desktop_ui.py", "ui/desktop.css",
                "../android/app/src/main/res/drawable/quantway_brand.png"}
    required.update("build/" + name for name in names)
    if not required.issubset(expected) or len(expected) > 256:
        fail("неполный SHA256SUMS; используйте архив build-linux-client.sh")
    # Keep the exact verified executable bytes in memory until root staging is complete.
    binaries = {}
    for relative, checksum in expected.items():
        content = read_source(relative, 128 * 1024 * 1024)
        if digest(content) != checksum:
            fail("контрольная сумма не совпала: " + relative)
        if relative.startswith("build/"):
            binaries[relative.removeprefix("build/")] = content
    arch = binaries["ARCH"].decode("ascii").strip()
    machine = {"arm64": 183, "amd64": 62}.get(arch)
    if machine is None:
        fail("неизвестная архитектура архива")
    for name in (*names, "quantway-helper"):
        content = binaries[name]
        if (content[:6] != b"\x7fELF\x02\x01" or len(content) < 20
                or int.from_bytes(content[18:20], "little") != machine):
            fail("неверная Linux-архитектура: " + name)
    if mode == "check":
        print("Архив проверен: linux/" + arch + ".")
        sys.exit(0)
    if os.geteuid() != 0 or platform.system() != "Linux":
        fail("установка выполняется администратором в Linux")
    if {"aarch64": "arm64", "arm64": "arm64", "x86_64": "amd64"}.get(platform.machine()) != arch:
        fail("архитектура архива не совпадает с компьютером")
    target = Path("/usr/libexec/quantway")
    current = Path("/")
    for part in ("", "usr", "libexec", "quantway"):
        if part:
            current = current / part
        try:
            info = os.lstat(current)
        except FileNotFoundError:
            os.mkdir(current, 0o755)
            os.chown(current, 0, 0)
            # mkdir is filtered by the invoking administrator's umask.
            os.chmod(current, 0o755)
            info = os.lstat(current)
        if (not stat.S_ISDIR(info.st_mode) or info.st_uid != 0
                or info.st_mode & 0o022):
            fail("небезопасный каталог установки: " + str(current))
        if current == target:
            # This directory belongs to this installer; repair older 0700 installs.
            os.chmod(current, 0o755)
        elif not info.st_mode & 0o001:
            fail("каталог установки недоступен обычному пользователю: " + str(current))
    for name in names:
        path = target / name
        try:
            info = os.lstat(path)
        except FileNotFoundError:
            continue
        if (not stat.S_ISREG(info.st_mode) or info.st_uid != 0
                or info.st_mode & 0o022):
            fail("небезопасный установленный файл: " + str(path))
    stage = Path(tempfile.mkdtemp(prefix=".install-", dir=target))
    installed = []
    try:
        for name in names:
            path = stage / name
            with path.open("xb") as stream:
                stream.write(binaries[name])
                stream.flush()
                os.fsync(stream.fileno())
            os.chown(path, 0, 0)
            os.chmod(path, 0o755)
            if digest(path.read_bytes()) != expected["build/" + name]:
                fail("ошибка проверки подготовленного файла: " + name)
            old = target / name
            if old.exists():
                shutil.copyfile(old, stage / (name + ".previous"), follow_symlinks=False)
                os.chmod(stage / (name + ".previous"), stat.S_IMODE(os.lstat(old).st_mode))
        try:
            for name in names:
                os.replace(stage / name, target / name)
                installed.append(name)
        except BaseException:
            for name in reversed(installed):
                backup = stage / (name + ".previous")
                if backup.exists():
                    os.replace(backup, target / name)
                else:
                    os.unlink(target / name)
            raise
    finally:
        shutil.rmtree(stage)
    print("Сетевые помощники установлены. Запустите приложение обычным пользователем: ./run.sh")
except (OSError, RuntimeError, UnicodeError, KeyError) as error:
    print("Установка остановлена: " + str(error), file=sys.stderr)
    sys.exit(1)
PY
