"""Inline in maintainer scripts; only inspect trusted paths and the VPN lock.

This is a one-time admission check, not a transaction-wide dpkg lock. It must
not create/remove the shared lock, change the network, or terminate a process.
Only Python's minimal standard-library modules are needed before unpacking.
"""
import errno
import fcntl
import os
import stat
import sys


LOCK = "/run/quantway-desktop-vpn.lock"
BINARIES = (
    "/usr/bin/quantway",
    "/usr/libexec/quantway/quantway-helper",
    "/usr/libexec/quantway/quantway-net-helper",
    "/usr/libexec/quantway/quantway-tinc-helper",
    "/usr/libexec/quantway/tinc",
    "/usr/libexec/quantway/tincd",
)
DIRECTORIES = (
    "/", "/usr", "/usr/bin", "/usr/libexec", "/usr/libexec/quantway",
    "/usr/share", "/usr/share/quantway", "/usr/share/quantway/mobile",
    "/usr/share/quantway/mobile/linux", "/usr/share/quantway/mobile/linux/ui",
    "/usr/share/quantway/mobile/android", "/usr/share/quantway/mobile/android/app",
    "/usr/share/quantway/mobile/android/app/src",
    "/usr/share/quantway/mobile/android/app/src/main",
    "/usr/share/quantway/mobile/android/app/src/main/res",
    "/usr/share/quantway/mobile/android/app/src/main/res/drawable",
)


def fail(message):
    raise RuntimeError(message)


def existing(path):
    try:
        return os.lstat(path)
    except FileNotFoundError:
        return None


def directory(path):
    info = existing(path)
    if info is not None and (not stat.S_ISDIR(info.st_mode) or info.st_uid != 0 or
                             stat.S_IMODE(info.st_mode) & 0o022):
        fail("unsafe installation directory: " + path)


def check_install_paths():
    for path in DIRECTORIES:
        directory(path)
    for path in BINARIES:
        info = existing(path)
        if info is not None and (not stat.S_ISREG(info.st_mode) or info.st_uid != 0 or
                                 stat.S_IMODE(info.st_mode) & 0o7022 or info.st_nlink != 1):
            fail("unsafe existing executable: " + path)


def secure_lock(info):
    return (stat.S_ISREG(info.st_mode) and info.st_uid == 0 and info.st_gid == 0 and
            stat.S_IMODE(info.st_mode) == 0o600 and info.st_nlink == 1)


def check_idle():
    directory("/")
    directory("/run")
    before = existing(LOCK)
    if before is None:
        return
    if not secure_lock(before):
        fail("unsafe desktop VPN lock; no package action was taken")
    fd = os.open(LOCK, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK | os.O_CLOEXEC)
    try:
        opened = os.fstat(fd)
        if not secure_lock(opened) or (opened.st_dev, opened.st_ino) != (before.st_dev, before.st_ino):
            fail("desktop VPN lock changed while checking; close Quantway and retry")
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except OSError as error:
            if error.errno in (errno.EAGAIN, errno.EACCES):
                fail("disconnect the VPN and close Quantway before installing, upgrading or removing it")
            raise
        current = existing(LOCK)
        if current is None or not secure_lock(current) or (current.st_dev, current.st_ino) != (opened.st_dev, opened.st_ino):
            fail("desktop VPN lock changed while checking; close Quantway and retry")
    finally:
        os.close(fd)


def main():
    if os.geteuid() != 0:
        fail("package maintenance requires administrator privileges")
    if len(sys.argv) != 2 or sys.argv[1] not in ("preinst", "prerm"):
        fail("invalid maintainer check invocation")
    if sys.argv[1] == "preinst":
        check_install_paths()
    check_idle()


try:
    main()
except (OSError, RuntimeError) as error:
    print("Quantway: " + str(error), file=sys.stderr)
    sys.exit(1)
