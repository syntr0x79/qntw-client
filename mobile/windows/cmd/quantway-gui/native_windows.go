//go:build windows

package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/danieljoos/wincred"
	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	comdlg32 = windows.NewLazySystemDLL("comdlg32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	dwmapi   = windows.NewLazySystemDLL("dwmapi.dll")

	procFindWindowW          = user32.NewProc("FindWindowW")
	procShowWindow           = user32.NewProc("ShowWindow")
	procSetForegroundWindow  = user32.NewProc("SetForegroundWindow")
	procMessageBoxW          = user32.NewProc("MessageBoxW")
	procDestroyWindow        = user32.NewProc("DestroyWindow")
	procReleaseCapture       = user32.NewProc("ReleaseCapture")
	procSendMessageW         = user32.NewProc("SendMessageW")
	procPostMessageW         = user32.NewProc("PostMessageW")
	procSetWindowLongPtrW    = user32.NewProc("SetWindowLongPtrW")
	procGetWindowLongPtrW    = user32.NewProc("GetWindowLongPtrW")
	procCallWindowProcW      = user32.NewProc("CallWindowProcW")
	procSetWindowPos         = user32.NewProc("SetWindowPos")
	procIsZoomed             = user32.NewProc("IsZoomed")
	procGetSystemMetrics     = user32.NewProc("GetSystemMetrics")
	procGetDpiForWindow      = user32.NewProc("GetDpiForWindow")
	procCreatePopupMenu      = user32.NewProc("CreatePopupMenu")
	procAppendMenuW          = user32.NewProc("AppendMenuW")
	procTrackPopupMenu       = user32.NewProc("TrackPopupMenu")
	procDestroyMenu          = user32.NewProc("DestroyMenu")
	procGetCursorPos         = user32.NewProc("GetCursorPos")
	procLoadImageW           = user32.NewProc("LoadImageW")
	procOpenClipboard        = user32.NewProc("OpenClipboard")
	procCloseClipboard       = user32.NewProc("CloseClipboard")
	procEmptyClipboard       = user32.NewProc("EmptyClipboard")
	procSetClipboardData     = user32.NewProc("SetClipboardData")
	procShellExecuteW        = shell32.NewProc("ShellExecuteW")
	procShellNotifyIconW     = shell32.NewProc("Shell_NotifyIconW")
	procGetOpenFileNameW     = comdlg32.NewProc("GetOpenFileNameW")
	procGetSaveFileNameW     = comdlg32.NewProc("GetSaveFileNameW")
	procGlobalAlloc          = kernel32.NewProc("GlobalAlloc")
	procGlobalLock           = kernel32.NewProc("GlobalLock")
	procGlobalUnlock         = kernel32.NewProc("GlobalUnlock")
	procDwmExtendFrame       = dwmapi.NewProc("DwmExtendFrameIntoClientArea")
	procDwmSetWindowAttribute = dwmapi.NewProc("DwmSetWindowAttribute")
)

const (
	swHide     = 0
	swShow     = 5
	swMinimize = 6
	swMaximize = 3
	swRestore  = 9

	wmClose         = 0x0010
	wmActivateApp   = 0x001C
	wmNCCalcSize    = 0x0083
	wmNCLButtonDown = 0x00A1
	wmCommand       = 0x0111
	wmSysCommand    = 0x0112
	wmLButtonUp     = 0x0202
	wmRButtonUp     = 0x0205
	wmLButtonDblClk = 0x0203
	wmTray          = 0x8000 + 41 // WM_APP + 41

	scRestore = 0xF120

	htCaption     = 2
	htLeft        = 10
	htRight       = 11
	htTop         = 12
	htTopLeft     = 13
	htTopRight    = 14
	htBottom      = 15
	htBottomLeft  = 16
	htBottomRight = 17

	gwlpWndProc = ^uintptr(3) // -4
	gwlStyle    = ^uintptr(15) // -16
)

// ---------------------------------------------------------------- window

var (
	hookedApp *app
	oldProc   uintptr
	quitting  bool
)

// installWindowHooks makes the window frameless (the HTML draws its own
// controls, like the hidden-titlebar macOS window) and routes close, tray and
// activation messages to the app.
func installWindowHooks(a *app) {
	hookedApp = a
	oldProc, _, _ = procGetWindowLongPtrW.Call(a.hwnd, gwlpWndProc)
	procSetWindowLongPtrW.Call(a.hwnd, gwlpWndProc, syscall.NewCallback(windowProc))
	// Keep DWM shadow and Windows 11 rounded corners on the frameless window.
	margins := [4]int32{0, 0, 1, 0}
	procDwmExtendFrame.Call(a.hwnd, uintptr(unsafe.Pointer(&margins)))
	corner := uint32(2) // DWMWCP_ROUND
	procDwmSetWindowAttribute.Call(a.hwnd, 33, uintptr(unsafe.Pointer(&corner)), 4)
	dark := uint32(1)
	procDwmSetWindowAttribute.Call(a.hwnd, 20, uintptr(unsafe.Pointer(&dark)), 4)
	const swpFlags = 0x0001 | 0x0002 | 0x0004 | 0x0020 // NOSIZE|NOMOVE|NOZORDER|FRAMECHANGED
	procSetWindowPos.Call(a.hwnd, 0, 0, 0, 0, 0, swpFlags)
}

type nccalcsizeParams struct {
	Rgrc [3]windows.Rect
	Pos  uintptr
}

func windowProc(hwnd, msg, wp, lp uintptr) uintptr {
	a := hookedApp
	switch msg {
	case wmNCCalcSize:
		if wp != 0 {
			if zoomed, _, _ := procIsZoomed.Call(hwnd); zoomed != 0 {
				// A maximized window extends past the monitor by its frame size.
				params := (*nccalcsizeParams)(unsafe.Pointer(lp))
				frame := frameThickness(hwnd)
				params.Rgrc[0].Left += frame
				params.Rgrc[0].Top += frame
				params.Rgrc[0].Right -= frame
				params.Rgrc[0].Bottom -= frame
			}
			return 0
		}
	case wmClose:
		if !quitting && a != nil {
			a.onClose()
			return 0
		}
	case wmActivateApp:
		if wp != 0 && a != nil && a.w != nil {
			a.w.Eval("window.appBecameActive&&window.appBecameActive()")
		}
	case wmTray:
		if a != nil {
			switch lp & 0xFFFF {
			case wmLButtonUp, wmLButtonDblClk:
				a.showWindow()
			case wmRButtonUp:
				a.tray.menu(hwnd)
			}
		}
		return 0
	case wmCommand:
		if a != nil && wp>>16 == 0 {
			switch wp & 0xFFFF {
			case trayOpen:
				a.showWindow()
				return 0
			case trayDisconnect:
				a.stopVPN()
				return 0
			case trayQuit:
				a.requestQuit()
				return 0
			}
		}
	}
	r, _, _ := procCallWindowProcW.Call(oldProc, hwnd, msg, wp, lp)
	if msg == 0x0005 && a != nil && a.w != nil { // WM_SIZE: let the page drop resize edges when maximized
		zoomed, _, _ := procIsZoomed.Call(hwnd)
		if zoomed != 0 {
			a.w.Eval("window.__qwMaximized=true")
		} else {
			a.w.Eval("window.__qwMaximized=false")
		}
	}
	return r
}

func frameThickness(hwnd uintptr) int32 {
	dpi, _, _ := procGetDpiForWindow.Call(hwnd)
	if dpi == 0 {
		dpi = 96
	}
	cx, _, _ := procGetSystemMetrics.Call(32)  // SM_CXFRAME
	pad, _, _ := procGetSystemMetrics.Call(92) // SM_CXPADDEDBORDER
	return int32((cx + pad) * dpi / 96)
}

func startWindowDrag(hwnd uintptr) {
	procReleaseCapture.Call()
	procSendMessageW.Call(hwnd, wmNCLButtonDown, htCaption, 0)
}

func startWindowResize(hwnd uintptr, edge string) {
	hit := map[string]uintptr{"top": htTop, "bottom": htBottom, "left": htLeft, "right": htRight, "topleft": htTopLeft, "topright": htTopRight, "bottomleft": htBottomLeft, "bottomright": htBottomRight}[edge]
	if hit == 0 {
		return
	}
	procReleaseCapture.Call()
	procSendMessageW.Call(hwnd, wmNCLButtonDown, hit, 0)
}

func toggleMaximize(hwnd uintptr) {
	if zoomed, _, _ := procIsZoomed.Call(hwnd); zoomed != 0 {
		procShowWindow.Call(hwnd, swRestore)
	} else {
		procShowWindow.Call(hwnd, swMaximize)
	}
}

// ---------------------------------------------------------------- tray

const (
	trayOpen       = 41001
	trayDisconnect = 41002
	trayQuit       = 41003
)

type notifyIconData struct {
	Size            uint32
	Wnd             uintptr
	ID              uint32
	Flags           uint32
	CallbackMessage uint32
	Icon            uintptr
	Tip             [128]uint16
	State           uint32
	StateMask       uint32
	Info            [256]uint16
	Version         uint32
	InfoTitle       [64]uint16
	InfoFlags       uint32
	GUIDItem        windows.GUID
	BalloonIcon     uintptr
}

type trayIcon struct {
	mu      sync.Mutex
	hwnd    uintptr
	added   bool
	status  string
	enabled bool
}

func newTray(hwnd uintptr) *trayIcon { return &trayIcon{hwnd: hwnd, status: "VPN выключен"} }

func (t *trayIcon) data() notifyIconData {
	var hinstance windows.Handle
	_ = windows.GetModuleHandleEx(0, nil, &hinstance)
	icon, _, _ := procLoadImageW.Call(uintptr(hinstance), 1, 1, 16, 16, 0)
	d := notifyIconData{Wnd: t.hwnd, ID: 1, Flags: 0x1 | 0x2 | 0x4, CallbackMessage: wmTray, Icon: icon}
	d.Size = uint32(unsafe.Sizeof(d))
	tip, _ := windows.UTF16FromString("Quantway — " + t.status)
	copy(d.Tip[:len(d.Tip)-1], tip)
	return d
}

// show adds the notification-area icon (the macOS status item equivalent).
func (t *trayIcon) show() {
	t.mu.Lock()
	defer t.mu.Unlock()
	d := t.data()
	if t.added {
		procShellNotifyIconW.Call(1, uintptr(unsafe.Pointer(&d))) // NIM_MODIFY
		return
	}
	if r, _, _ := procShellNotifyIconW.Call(0, uintptr(unsafe.Pointer(&d))); r != 0 { // NIM_ADD
		t.added = true
	}
}

func (t *trayIcon) setStatus(status string, connected bool) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.status, t.enabled = status, connected
	added := t.added
	t.mu.Unlock()
	if added {
		t.show()
	} else if status != "VPN выключен" {
		t.show() // the icon stays visible while a VPN session exists
	}
}

func (t *trayIcon) remove() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.added {
		return
	}
	d := t.data()
	procShellNotifyIconW.Call(2, uintptr(unsafe.Pointer(&d))) // NIM_DELETE
	t.added = false
}

func (t *trayIcon) menu(hwnd uintptr) {
	t.mu.Lock()
	status := t.status
	t.mu.Unlock()
	m, _, _ := procCreatePopupMenu.Call()
	if m == 0 {
		return
	}
	defer procDestroyMenu.Call(m)
	add := func(flags uintptr, id uintptr, text string) {
		p, _ := windows.UTF16PtrFromString(text)
		procAppendMenuW.Call(m, flags, id, uintptr(unsafe.Pointer(p)))
	}
	add(0x1, 0, status) // MF_GRAYED
	add(0, trayOpen, "Открыть Quantway")
	add(0, trayDisconnect, "Отключить VPN")
	add(0x800, 0, "") // MF_SEPARATOR
	add(0, trayQuit, "Завершить Quantway")
	var pt struct{ X, Y int32 }
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWindow.Call(hwnd)
	procTrackPopupMenu.Call(m, 0x0020, uintptr(pt.X), uintptr(pt.Y), 0, hwnd, 0) // TPM_BOTTOMALIGN
	procPostMessageW.Call(hwnd, 0, 0, 0)
}

// ---------------------------------------------------------------- shell

func shellOpen(target string) {
	verb, _ := windows.UTF16PtrFromString("open")
	file, _ := windows.UTF16PtrFromString(target)
	procShellExecuteW.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(file)), 0, 0, swShow)
}

func setClipboard(text string) error {
	data, err := windows.UTF16FromString(text)
	if err != nil {
		return err
	}
	if r, _, _ := procOpenClipboard.Call(0); r == 0 {
		return errors.New("clipboard busy")
	}
	defer procCloseClipboard.Call()
	procEmptyClipboard.Call()
	size := uintptr(len(data) * 2)
	h, _, _ := procGlobalAlloc.Call(0x0002, size) // GMEM_MOVEABLE
	if h == 0 {
		return errors.New("alloc failed")
	}
	p, _, _ := procGlobalLock.Call(h)
	copy(unsafe.Slice((*uint16)(unsafe.Pointer(p)), len(data)), data)
	procGlobalUnlock.Call(h)
	if r, _, _ := procSetClipboardData.Call(13, h); r == 0 { // CF_UNICODETEXT
		return errors.New("clipboard write failed")
	}
	return nil
}

// ---------------------------------------------------------------- dialogs

type openFileName struct {
	StructSize      uint32
	Owner           uintptr
	Instance        uintptr
	Filter          *uint16
	CustomFilter    *uint16
	MaxCustFilter   uint32
	FilterIndex     uint32
	File            *uint16
	MaxFile         uint32
	FileTitle       *uint16
	MaxFileTitle    uint32
	InitialDir      *uint16
	Title           *uint16
	Flags           uint32
	FileOffset      uint16
	FileExtension   uint16
	DefExt          *uint16
	CustData        uintptr
	FnHook          uintptr
	TemplateName    *uint16
	PvReserved      uintptr
	DwReserved      uint32
	FlagsEx         uint32
}

func filter(parts ...string) *uint16 {
	var b []uint16
	for _, p := range parts {
		u, _ := windows.UTF16FromString(p)
		b = append(b, u...)
	}
	b = append(b, 0)
	return &b[0]
}

func openFileDialog(owner uintptr, multi bool) ([]string, error) {
	buffer := make([]uint16, 64<<10)
	ofn := openFileName{Owner: owner, Filter: filter("Изображения (PNG, JPEG)", "*.png;*.jpg;*.jpeg"), File: &buffer[0], MaxFile: uint32(len(buffer))}
	ofn.StructSize = uint32(unsafe.Sizeof(ofn))
	ofn.Flags = 0x00080000 | 0x00001000 | 0x00000800 | 0x00000008 // EXPLORER|FILEMUSTEXIST|PATHMUSTEXIST|NOCHANGEDIR
	if multi {
		ofn.Flags |= 0x00000200 // ALLOWMULTISELECT
	}
	if r, _, _ := procGetOpenFileNameW.Call(uintptr(unsafe.Pointer(&ofn))); r == 0 {
		return nil, nil
	}
	// Multi-select returns "dir\0file1\0file2\0\0"; single returns a full path.
	var items []string
	start := 0
	for i, c := range buffer {
		if c == 0 {
			if i == start {
				break
			}
			items = append(items, windows.UTF16ToString(buffer[start:i]))
			start = i + 1
		}
	}
	if len(items) <= 1 {
		return items, nil
	}
	dir := items[0]
	var paths []string
	for _, name := range items[1:] {
		paths = append(paths, filepath.Join(dir, name))
	}
	return paths, nil
}

func saveFileDialog(owner uintptr, name string, png bool) (string, error) {
	buffer := make([]uint16, 32768)
	initial, _ := windows.UTF16FromString(name)
	copy(buffer, initial)
	ext := "png"
	f := filter("PNG", "*.png")
	if !png {
		ext = "jpg"
		f = filter("JPEG", "*.jpg;*.jpeg")
	}
	def, _ := windows.UTF16PtrFromString(ext)
	ofn := openFileName{Owner: owner, Filter: f, File: &buffer[0], MaxFile: uint32(len(buffer)), DefExt: def}
	ofn.StructSize = uint32(unsafe.Sizeof(ofn))
	ofn.Flags = 0x00080000 | 0x00000002 | 0x00000800 | 0x00000008 // EXPLORER|OVERWRITEPROMPT|PATHMUSTEXIST|NOCHANGEDIR
	if r, _, _ := procGetSaveFileNameW.Call(uintptr(unsafe.Pointer(&ofn))); r == 0 {
		return "", nil
	}
	return windows.UTF16ToString(buffer), nil
}

// ---------------------------------------------------------------- secrets

// The session cookie uses the same Credential Manager entry as the previous
// Windows client, so an existing login carries over. Larger values (external
// subscriptions) exceed the 2560-byte credential limit and are stored with
// DPAPI, bound to the current Windows user.
const sessionTarget = "link.quantway.desktop/account-session/v1"

func credRead(account string) (string, error) {
	if account == "session" {
		c, err := wincred.GetGenericCredential(sessionTarget)
		if err != nil {
			return "", err
		}
		return string(c.CredentialBlob), nil
	}
	data, err := os.ReadFile(secretPath(account))
	if err != nil {
		return "", err
	}
	in := windows.DataBlob{Size: uint32(len(data)), Data: &data[0]}
	var out windows.DataBlob
	if err = windows.CryptUnprotectData(&in, nil, nil, 0, nil, 0x1, &out); err != nil {
		return "", err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return string(unsafe.Slice(out.Data, out.Size)), nil
}

func credWrite(account, value string) error {
	if account == "session" {
		if len(value) == 0 || len(value) > 2560 {
			return errors.New("invalid credential size")
		}
		c := wincred.NewGenericCredential(sessionTarget)
		c.CredentialBlob = []byte(value)
		c.Comment = "Quantway — вход в аккаунт"
		c.Persist = wincred.PersistLocalMachine
		return c.Write()
	}
	plain := []byte(value)
	if len(plain) == 0 {
		plain = []byte("[]")
	}
	in := windows.DataBlob{Size: uint32(len(plain)), Data: &plain[0]}
	var out windows.DataBlob
	description, _ := windows.UTF16PtrFromString("Quantway")
	if err := windows.CryptProtectData(&in, description, nil, 0, nil, 0x1, &out); err != nil {
		return err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	path := secretPath(account)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, unsafe.Slice(out.Data, out.Size), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func credDelete(account string) {
	if account == "session" {
		if c, err := wincred.GetGenericCredential(sessionTarget); err == nil {
			_ = c.Delete()
		}
		return
	}
	_ = os.Remove(secretPath(account))
}

func secretPath(account string) string {
	dir, _ := appDataDir()
	return filepath.Join(dir, "Secrets", strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' {
			return r
		}
		return '_'
	}, account)+".dpapi")
}
