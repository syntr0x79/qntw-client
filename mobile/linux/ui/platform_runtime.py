"""Small desktop OS adapters shared by the Linux and Windows GTK shells."""
import os
from pathlib import Path
import sys


def is_windows():
    return sys.platform == "win32"


def platform_label():
    return "Windows" if is_windows() else "Linux"


def helper_path():
    configured = os.environ.get("QUANTWAY_HELPER")
    if configured:
        return configured
    ui = Path(__file__).resolve().parent
    if is_windows():
        return str(ui.parent / "bin" / "quantway-helper.exe")
    return str(ui.parent / "build" / "quantway-helper")


def helper_process_options():
    # Go always writes UTF-8 JSON. A Windows locale's legacy codepage must
    # neither corrupt Cyrillic messages nor create a console for each request.
    if is_windows():
        return {"creationflags": 0x08000000, "encoding": "utf-8"}
    return {}


def brand_image():
    configured = os.environ.get("QUANTWAY_ASSET_DIR")
    if configured:
        return Path(configured) / "quantway_brand.png"
    return Path(__file__).resolve().parents[2] / "android/app/src/main/res/drawable/quantway_brand.png"
