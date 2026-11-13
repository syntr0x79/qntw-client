//go:build windows

// Quantway desktop for Windows: the same HTML interface as the macOS client
// (mobile/macos/Sources/QuantwayApp/Resources/UI), hosted in WebView2. The
// native side mirrors QuantwayApp.swift: it runs the unprivileged
// quantway-helper.exe for account/probe/VPN actions and never runs elevated.
package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
)

const windowTitle = "Quantway"

func main() {
	executable, err := os.Executable()
	if err != nil {
		fatal(err)
	}
	root := filepath.Dir(executable)
	index := filepath.Join(root, "ui", "index.html")
	helper := filepath.Join(root, "bin", "quantway-helper.exe")
	for _, path := range []string{index, helper} {
		if info, e := os.Stat(path); e != nil || !info.Mode().IsRegular() {
			fatal(fmt.Errorf("Неполная установка Quantway: %s", filepath.Base(path)))
		}
	}
	if !singleInstance() {
		return
	}
	data, err := appDataDir()
	if err != nil {
		fatal(err)
	}
	app := newApp(root, helper, data)
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		Debug:     os.Getenv("QUANTWAY_DEVTOOLS") == "1",
		AutoFocus: true,
		DataPath:  filepath.Join(data, "WebView2"),
		WindowOptions: webview2.WindowOptions{
			Title: windowTitle, Width: 1100, Height: 700, IconId: 1, Center: true,
		},
	})
	if w == nil {
		fatal(fmt.Errorf("Не удалось запустить WebView2. Установите Microsoft Edge WebView2 Runtime и откройте Quantway снова."))
	}
	app.attach(w)
	w.SetSize(960, 640, webview2.HintMin)
	if err = w.Bind("__qwSend", app.receive); err != nil {
		fatal(err)
	}
	w.Init(bridgeScript)
	target := url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(index)}
	w.Navigate(target.String())
	w.Run()
	app.shutdown()
}

func appDataDir() (string, error) {
	base, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, 0)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "Quantway")
	return dir, os.MkdirAll(dir, 0o700)
}

// singleInstance keeps one GUI per interactive session and focuses the
// existing window when Quantway is opened again.
func singleInstance() bool {
	name, _ := windows.UTF16PtrFromString(`Local\Quantway.Desktop.GUI`)
	h, err := windows.CreateMutex(nil, true, name)
	if err == nil {
		return true // handle intentionally kept until exit
	}
	if h != 0 {
		windows.CloseHandle(h)
	}
	if err == windows.ERROR_ALREADY_EXISTS {
		title, _ := windows.UTF16PtrFromString(windowTitle)
		class, _ := windows.UTF16PtrFromString("webview")
		hwnd, _, _ := procFindWindowW.Call(uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(title)))
		if hwnd != 0 {
			procShowWindow.Call(hwnd, swRestore)
			procSetForegroundWindow.Call(hwnd)
		}
		return false
	}
	return true
}

func fatal(err error) {
	message, _ := syscall.UTF16PtrFromString(err.Error())
	title, _ := syscall.UTF16PtrFromString(windowTitle)
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(message)), uintptr(unsafe.Pointer(title)), 0x10)
	os.Exit(1)
}

// bridgeScript emulates WKScriptMessageHandlerWithReply so the macOS UI runs
// unchanged, and adds dragging/resizing for the frameless window.
var bridgeScript = strings.TrimSpace(`
(() => {
  if (window.top !== window) return;
  const pending = new Map(); let seq = 0;
  window.__qwReply = (id, ok, value) => {
    const p = pending.get(id); if (!p) return; pending.delete(id);
    ok ? p.resolve(value) : p.reject(Error(String(value || 'Ошибка')));
  };
  const post = body => new Promise((resolve, reject) => {
    const id = ++seq; pending.set(id, {resolve, reject});
    window.__qwSend(id, body).catch(e => { pending.delete(id); reject(Error(String(e))); });
  });
  window.webkit = {messageHandlers: {quantway: {postMessage: post}}};
  window.QUANTWAY_PLATFORM = 'windows';
  const native = (action, body) => window.__qwSend(0, {action, body: body || {}}).catch(() => {});
  const edgeAt = e => {
    if (window.__qwMaximized) return '';
    const b = 6, w = innerWidth, h = innerHeight, x = e.clientX, y = e.clientY;
    let v = y < b ? 'top' : y > h - b ? 'bottom' : '', s = x < b ? 'left' : x > w - b ? 'right' : '';
    return v + s;
  };
  const cursors = {top:'ns-resize',bottom:'ns-resize',left:'ew-resize',right:'ew-resize',topleft:'nwse-resize',bottomright:'nwse-resize',topright:'nesw-resize',bottomleft:'nesw-resize'};
  const interactive = 'button,a,input,textarea,select,label,option,[role=button],[role=dialog] *,[contenteditable],[tabindex]';
  const dragZone = e => e.clientY < 48 || e.target.closest('nav,header,[data-drag]');
  addEventListener('mousemove', e => {
    const edge = edgeAt(e);
    document.documentElement.style.cursor = edge ? cursors[edge] : '';
  }, true);
  addEventListener('mousedown', e => {
    if (e.button !== 0) return;
    const edge = edgeAt(e);
    if (edge) { e.preventDefault(); e.stopPropagation(); native('__resize', {edge}); return; }
    if (e.target.closest(interactive) || !dragZone(e)) return;
    if (e.detail === 2) { native('__maximize'); return; }
    native('__drag');
  }, true);
  addEventListener('keydown', e => {
    if (e.key === 'F5' || (e.ctrlKey && (e.key === 'r' || e.key === 'R'))) e.preventDefault();
  }, true);
})();
`)
