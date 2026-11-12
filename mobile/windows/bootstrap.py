"""Trusted bootstrap executed by bundled Python in isolated mode (-I)."""
import ctypes
import os
from pathlib import Path
import runpy
import sys
import traceback

ROOT = Path(__file__).resolve().parent
RUNTIME = ROOT / "runtime"
# Do not consume an externally supplied install directory or Python path.
sys.path.insert(0, str(ROOT / "ui"))
DLL_HANDLES = [os.add_dll_directory(str(RUNTIME / "bin"))]
try:
    ctypes.windll.user32.SetProcessDpiAwarenessContext(ctypes.c_void_p(-4))
except (AttributeError, OSError):
    pass

def prepare_pixbuf_cache():
    # Install data is trusted and immutable. The image-loader cache is private
    # to this user and contains only absolute paths to bundled modules.
    sys.path.insert(1, str(ROOT))
    from pixbuf_cache import prepare_cache, LOADERS
    cache_dir = Path(os.environ["LOCALAPPDATA"]) / "Quantway" / "cache"
    def warning(message):
        directory = Path(os.environ["LOCALAPPDATA"]) / "Quantway" / "logs"
        directory.mkdir(parents=True, exist_ok=True)
        with (directory / "startup-warning.log").open("a", encoding="utf-8") as stream:
            stream.write(message + "\n")
    cache, source = prepare_cache(ROOT, cache_dir, warn=warning)
    os.environ["GDK_PIXBUF_MODULE_FILE"] = str(cache)
    os.environ["GDK_PIXBUF_MODULEDIR"] = str(ROOT / LOADERS)
    status = Path(os.environ["LOCALAPPDATA"]) / "Quantway" / "logs"
    status.mkdir(parents=True, exist_ok=True)
    (status / "bootstrap-status.json").write_text(__import__("json").dumps({"pid": os.getpid(), "loader_cache_source": source, "bootstrap_ready": True}) + "\n", encoding="utf-8")

try:
    prepare_pixbuf_cache()
    sys.argv = [str(ROOT / "ui" / "quantway.py")]
    runpy.run_path(sys.argv[0], run_name="__main__")
except SystemExit:
    raise
except BaseException:
    log_dir = Path(os.environ["LOCALAPPDATA"]) / "Quantway" / "logs"
    log_dir.mkdir(parents=True, exist_ok=True)
    (log_dir / "startup.log").write_text(traceback.format_exc(), encoding="utf-8")
    raise
