//go:build windows

package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	webview2 "github.com/jchv/go-webview2"
	qrcode "github.com/skip2/go-qrcode"
	"golang.org/x/sys/windows"
)

type message struct {
	Action string         `json:"action"`
	Body   map[string]any `json:"body"`
}

type app struct {
	root, helper, data string
	w                  webview2.WebView
	hwnd               uintptr
	tray               *trayIcon

	mu            sync.Mutex
	cookie        string
	authEpoch     int
	loginProcess  *exec.Cmd
	probeProcess  *exec.Cmd
	vpn           *exec.Cmd
	vpnInput      io.WriteCloser
	vpnMode       string
	vpnDone       chan struct{}
	quitApproved  bool
	terminating   bool
	settings      settings
	settingsMutex sync.Mutex
}

type settings struct {
	CloseToTray       bool `json:"closeToTray"`
	AutologinDisabled bool `json:"autologinDisabled"`
}

func newApp(root, helper, data string) *app {
	a := &app{root: root, helper: helper, data: data}
	a.loadSettings()
	return a
}

func (a *app) attach(w webview2.WebView) {
	a.w = w
	a.hwnd = uintptr(w.Window())
	installWindowHooks(a)
	a.tray = newTray(a.hwnd)
	a.tray.show()
}

// ---------------------------------------------------------------- settings

func (a *app) settingsPath() string { return filepath.Join(a.data, "settings.json") }
func (a *app) loadSettings() {
	b, err := os.ReadFile(a.settingsPath())
	if err == nil {
		_ = json.Unmarshal(b, &a.settings)
	}
}
func (a *app) updateSettings(f func(*settings)) {
	a.settingsMutex.Lock()
	defer a.settingsMutex.Unlock()
	f(&a.settings)
	b, _ := json.Marshal(a.settings)
	_ = os.WriteFile(a.settingsPath(), b, 0o600)
}
func (a *app) setting() settings {
	a.settingsMutex.Lock()
	defer a.settingsMutex.Unlock()
	return a.settings
}

// ---------------------------------------------------------------- JS glue

func (a *app) send(function string, value any) {
	b, err := json.Marshal(value)
	if err != nil {
		return
	}
	script := "window." + function + "&&window." + function + "(" + string(b) + ")"
	a.w.Dispatch(func() { a.w.Eval(script) })
}

func (a *app) reply(id int, value any, failure string) {
	if id == 0 {
		return
	}
	var script string
	if failure != "" {
		b, _ := json.Marshal(failure)
		script = fmt.Sprintf("window.__qwReply(%d,false,%s)", id, b)
	} else {
		if value == nil {
			value = map[string]any{}
		}
		b, err := json.Marshal(value)
		if err != nil {
			b, _ = json.Marshal("Некорректный ответ.")
			script = fmt.Sprintf("window.__qwReply(%d,false,%s)", id, b)
		} else {
			script = fmt.Sprintf("window.__qwReply(%d,true,%s)", id, b)
		}
	}
	a.w.Dispatch(func() { a.w.Eval(script) })
}

// receive is called on the UI thread; anything slow runs in a goroutine.
func (a *app) receive(id int, m message) {
	if m.Body == nil {
		m.Body = map[string]any{}
	}
	ok := func(v any) { a.reply(id, v, "") }
	fail := func(s string) { a.reply(id, nil, s) }
	switch m.Action {
	case "__drag":
		startWindowDrag(a.hwnd)
	case "__resize":
		startWindowResize(a.hwnd, str(m.Body["edge"]))
	case "__maximize":
		toggleMaximize(a.hwnd)
	case "minimize":
		procShowWindow.Call(a.hwnd, swMinimize)
		ok(nil)
	case "close", "quit":
		a.mu.Lock()
		a.quitApproved = true
		a.mu.Unlock()
		ok(nil)
		a.requestQuit()
	case "hide-to-tray":
		if b, _ := m.Body["remember"].(bool); b {
			a.updateSettings(func(s *settings) { s.CloseToTray = true })
		}
		a.hideToTray()
		ok(nil)
	case "cancel-login":
		a.mu.Lock()
		a.authEpoch++
		if a.loginProcess != nil && a.loginProcess.Process != nil {
			_ = a.loginProcess.Process.Kill()
		}
		a.loginProcess = nil
		a.mu.Unlock()
		ok(nil)
	case "cancel-probe":
		a.mu.Lock()
		if a.probeProcess != nil && a.probeProcess.Process != nil {
			_ = a.probeProcess.Process.Kill()
		}
		a.probeProcess = nil
		a.mu.Unlock()
		ok(nil)
	case "retry-storage":
		a.mu.Lock()
		cookie := a.cookie
		a.mu.Unlock()
		if cookie == "" {
			fail("Войдите снова.")
			return
		}
		if credWrite("session", cookie) != nil {
			fail("Диспетчер учётных данных Windows по-прежнему недоступен.")
			return
		}
		a.updateSettings(func(s *settings) { s.AutologinDisabled = false })
		ok(nil)
	case "bootstrap":
		cookie := ""
		if !a.setting().AutologinDisabled {
			cookie, _ = credRead("session")
		}
		a.mu.Lock()
		a.cookie = cookie
		a.mu.Unlock()
		providers, _ := credRead("providers")
		var parsed []any
		if json.Unmarshal([]byte(providers), &parsed) != nil {
			parsed = []any{}
		}
		legacy, _ := credRead("subscription")
		ok(map[string]any{"hasSession": cookie != "", "providers": parsed, "legacyLink": legacy, "closeToTray": a.setting().CloseToTray})
	case "save-providers":
		providers := m.Body["providers"]
		if providers == nil {
			providers = []any{}
		}
		b, _ := json.Marshal(providers)
		if credWrite("providers", string(b)) != nil {
			fail("Не удалось сохранить подписки в диспетчере учётных данных Windows.")
			return
		}
		ok(nil)
	case "open":
		if u, err := url.Parse(str(m.Body["url"])); err == nil && (strings.EqualFold(u.Scheme, "https") || strings.EqualFold(u.Scheme, "tg")) {
			shellOpen(u.String())
		}
		ok(nil)
	case "qr":
		text := str(m.Body["text"])
		if text == "" || len([]rune(text)) > 8192 {
			fail("Не удалось создать QR-код.")
			return
		}
		png, err := qrcode.Encode(text, qrcode.Medium, 512)
		if err != nil {
			fail("Не удалось создать QR-код.")
			return
		}
		ok(map[string]any{"image": "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)})
	case "pick-images", "pick-image":
		limit := 1
		if v, ok := m.Body["limit"].(float64); ok {
			limit = max(1, min(5, int(v)))
		}
		multi := m.Action == "pick-images"
		go a.pickImages(id, multi, limit)
	case "save-image":
		go a.saveImage(id, m.Body)
	case "copy":
		if setClipboard(str(m.Body["text"])) != nil {
			fail("Не удалось скопировать.")
			return
		}
		ok(nil)
	case "start":
		a.mu.Lock()
		busy := a.vpn != nil
		a.mu.Unlock()
		if busy {
			fail("Сначала отключите текущее подключение.")
			return
		}
		a.startVPN(m.Body)
		ok(nil)
	case "stop":
		a.stopVPN()
		ok(nil)
	case "country":
		country := str(m.Body["country"])
		a.mu.Lock()
		input, mode := a.vpnInput, a.vpnMode
		a.mu.Unlock()
		if input == nil || mode != "tinc" || !regexp.MustCompile(`^[A-Z]{2}$`).MatchString(country) {
			fail("Смена страны доступна только в активном Tinc-подключении.")
			return
		}
		b, _ := json.Marshal(map[string]string{"action": "country", "country": country})
		if _, err := input.Write(append(b, '\n')); err != nil {
			fail("Не удалось передать команду смены страны.")
			return
		}
		ok(map[string]any{"accepted": true})
	case "logout":
		a.stopVPN()
		a.mu.Lock()
		a.authEpoch++
		previous := a.cookie
		a.cookie = ""
		a.mu.Unlock()
		a.updateSettings(func(s *settings) { s.AutologinDisabled = true })
		credDelete("session")
		go a.run("cabinet", map[string]any{"cookie": previous, "method": "POST", "path": "logout"}, nil)
		ok(nil)
	case "login", "restore-session", "cabinet", "profiles", "probe", "tinc-catalog", "account-refresh", "replace-device", "device-link", "plans", "invoice", "invoice-status":
		a.mu.Lock()
		if m.Action != "login" && m.Action != "profiles" && m.Action != "probe" && m.Action != "tinc-catalog" {
			m.Body["cookie"] = a.cookie
		}
		if m.Action == "login" {
			a.authEpoch++
		}
		epoch := a.authEpoch
		a.mu.Unlock()
		action, body := m.Action, m.Body
		go func() {
			response := a.run(action, body, nil)
			a.mu.Lock()
			current := a.authEpoch
			a.mu.Unlock()
			if epoch != current {
				ok(map[string]any{"error": "Вход отменён."})
				return
			}
			ok(a.filterSession(action, body, response))
		}()
	default:
		fail("Неизвестная операция.")
	}
}

// filterSession keeps the session cookie inside the native process, exactly
// like the macOS host.
func (a *app) filterSession(action string, body, response map[string]any) map[string]any {
	safe := map[string]any{}
	for k, v := range response {
		safe[k] = v
	}
	if session, ok := response["session"].(map[string]any); ok {
		if value, ok := session["cookie"].(string); ok {
			delete(session, "cookie")
			a.mu.Lock()
			a.cookie = value
			a.mu.Unlock()
			if credWrite("session", value) != nil {
				safe["storageWarning"] = "Вход сохранён только до закрытия приложения: диспетчер учётных данных Windows недоступен."
			} else {
				a.updateSettings(func(s *settings) { s.AutologinDisabled = false })
			}
		}
		safe["session"] = session
	}
	if action == "cabinet" && str(body["path"]) == "token" {
		if data, ok := response["data"].(map[string]any); ok {
			if value, ok := data["session_cookie"].(string); ok {
				delete(data, "session_cookie")
				a.mu.Lock()
				a.cookie = value
				a.authEpoch++
				a.mu.Unlock()
				if credWrite("session", value) != nil {
					safe["storageWarning"] = "Не удалось сохранить новый вход в диспетчере учётных данных Windows."
				}
			} else {
				a.mu.Lock()
				a.cookie = ""
				a.mu.Unlock()
				credDelete("session")
			}
			safe["data"] = data
		}
	}
	if action == "restore-session" {
		if status, ok := response["status"].(float64); ok && int(status) == 401 {
			a.mu.Lock()
			a.cookie = ""
			a.mu.Unlock()
			credDelete("session")
		}
	}
	return safe
}

// ---------------------------------------------------------------- helper

func (a *app) helperCommand(args ...string) *exec.Cmd {
	cmd := exec.Command(a.helper, args...)
	cmd.Dir = filepath.Dir(a.helper)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	// The installed helper uses this PID to reach this GUI's administrator
	// broker: one UAC approval per GUI session instead of one per connection.
	cmd.Env = append(os.Environ(), fmt.Sprintf("QUANTWAY_GUI_PID=%d", os.Getpid()))
	return cmd
}

func (a *app) run(action string, body map[string]any, _ func()) map[string]any {
	args := []string{"--json", "--action", action}
	if action == "probe" {
		args = append(args, "--progress")
	}
	cmd := a.helperCommand(args...)
	var input []byte
	if action == "profiles" {
		input = []byte(str(body["link"]))
	} else {
		var err error
		if input, err = json.Marshal(body); err != nil {
			return map[string]any{"error": "Некорректный запрос."}
		}
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return map[string]any{"error": "Сетевой компонент отсутствует или недоступен."}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return map[string]any{"error": "Сетевой компонент отсутствует или недоступен."}
	}
	a.mu.Lock()
	if action == "login" {
		a.loginProcess = cmd
	}
	if action == "probe" {
		a.probeProcess = cmd
	}
	a.mu.Unlock()
	if err = cmd.Start(); err != nil {
		return map[string]any{"error": "Сетевой компонент отсутствует или недоступен."}
	}
	_, _ = stdin.Write(append(input, '\n'))
	_ = stdin.Close()
	timer := time.AfterFunc(180*time.Second, func() { _ = cmd.Process.Kill() })
	var response map[string]any
	if action == "probe" {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64<<10), 64<<20)
		for scanner.Scan() {
			var event map[string]any
			if json.Unmarshal(scanner.Bytes(), &event) != nil {
				continue
			}
			if event["type"] == "progress" {
				a.mu.Lock()
				current := a.probeProcess == cmd
				a.mu.Unlock()
				if current {
					a.send("probeEvent", event)
				}
			} else {
				response = event
			}
		}
	} else {
		b, _ := io.ReadAll(stdout)
		_ = json.Unmarshal(bytes.TrimSpace(b), &response)
	}
	_ = cmd.Wait()
	timer.Stop()
	a.mu.Lock()
	if a.loginProcess == cmd {
		a.loginProcess = nil
	}
	if a.probeProcess == cmd {
		a.probeProcess = nil
	}
	a.mu.Unlock()
	if response == nil {
		return map[string]any{"error": "Не удалось получить ответ. Проверьте подключение к сети."}
	}
	return response
}

// ---------------------------------------------------------------- VPN session

var trayTitles = map[string]string{"connected": "VPN подключён", "connecting": "Подключение…", "stopping": "Отключение…", "failed": "Ошибка подключения"}

func (a *app) startVPN(body map[string]any) {
	action := "connect"
	mode := "vpn"
	if str(body["mode"]) == "tinc" {
		action, mode = "tinc-connect", "tinc"
	}
	cmd := a.helperCommand("--json", "--action", action)
	stdin, err1 := cmd.StdinPipe()
	stdout, err2 := cmd.StdoutPipe()
	data, err3 := json.Marshal(body)
	if err := errors.Join(err1, err2, err3); err != nil || cmd.Start() != nil {
		a.send("sessionEvent", map[string]any{"status": "failed", "error": "Не удалось запустить сетевой компонент."})
		a.send("sessionEvent", map[string]any{"status": "ended", "exitCode": 1})
		return
	}
	done := make(chan struct{})
	a.mu.Lock()
	a.vpn, a.vpnInput, a.vpnMode, a.vpnDone = cmd, stdin, mode, done
	a.mu.Unlock()
	_, _ = stdin.Write(append(data, '\n'))
	go func() {
		defer close(done)
		log := a.diagnosticsLog()
		if log != nil {
			defer log.Close()
		}
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64<<10), 4<<20)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			var event map[string]any
			if json.Unmarshal(line, &event) != nil {
				continue
			}
			// Helper output is public lifecycle/telemetry only: no requests,
			// subscription URLs, cookies or keys.
			if log != nil {
				_, _ = log.Write(append(line, '\n'))
			}
			if status, ok := event["status"].(string); ok {
				title := trayTitles[status]
				if title == "" {
					title = "VPN: настройка соединения"
				}
				a.tray.setStatus(title, status == "connected")
			}
			a.send("sessionEvent", event)
		}
		_ = cmd.Wait()
		code := 0
		if cmd.ProcessState != nil {
			code = cmd.ProcessState.ExitCode()
		}
		a.mu.Lock()
		a.vpn, a.vpnInput, a.vpnMode = nil, nil, ""
		terminating := a.terminating
		a.mu.Unlock()
		a.tray.setStatus("VPN выключен", false)
		a.send("sessionEvent", map[string]any{"status": "ended", "exitCode": code})
		if terminating {
			a.w.Dispatch(func() { a.closeNow() })
		}
	}()
}

func (a *app) stopVPN() {
	a.mu.Lock()
	input := a.vpnInput
	a.vpnInput = nil
	a.mu.Unlock()
	if input != nil {
		_, _ = input.Write([]byte("stop\n"))
		_ = input.Close()
	}
}

func (a *app) diagnosticsLog() *os.File {
	dir := filepath.Join(a.data, "Diagnostics")
	if os.MkdirAll(dir, 0o700) != nil {
		return nil
	}
	f, err := os.OpenFile(filepath.Join(dir, "connection.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil
	}
	return f
}

// ---------------------------------------------------------------- lifecycle

func (a *app) vpnActive() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.vpn != nil
}

// onClose handles the window close button / Alt+F4 (windowShouldClose).
func (a *app) onClose() {
	if a.vpnActive() && a.setting().CloseToTray {
		a.hideToTray()
		return
	}
	a.send("lifecycleAction", "close")
}

// requestQuit mirrors applicationShouldTerminate: confirm when VPN is active,
// otherwise disconnect and wait for the helper before closing.
func (a *app) requestQuit() {
	a.mu.Lock()
	active, approved := a.vpn != nil, a.quitApproved
	a.mu.Unlock()
	if active && !approved {
		a.showWindow()
		a.send("lifecycleAction", "quit")
		return
	}
	if active {
		a.mu.Lock()
		a.terminating = true
		a.mu.Unlock()
		a.stopVPN()
		return
	}
	a.w.Dispatch(func() { a.closeNow() })
}

func (a *app) closeNow() {
	a.tray.remove()
	quitting = true
	procDestroyWindow.Call(a.hwnd)
}

func (a *app) hideToTray() {
	a.tray.show()
	procShowWindow.Call(a.hwnd, swHide)
}

func (a *app) showWindow() {
	procShowWindow.Call(a.hwnd, swRestore)
	procShowWindow.Call(a.hwnd, swShow)
	procSetForegroundWindow.Call(a.hwnd)
}

func (a *app) shutdown() {
	a.tray.remove()
	a.mu.Lock()
	done := a.vpnDone
	a.mu.Unlock()
	a.stopVPN()
	if done != nil {
		select {
		case <-done:
		case <-time.After(25 * time.Second):
		}
	}
}

// ---------------------------------------------------------------- files

func (a *app) pickImages(id int, multi bool, limit int) {
	paths, err := openFileDialog(a.hwnd, multi)
	if err != nil || len(paths) == 0 {
		a.reply(id, map[string]any{}, "")
		return
	}
	if len(paths) > limit {
		a.reply(id, nil, "Можно прикрепить не больше пяти изображений.")
		return
	}
	var images []map[string]any
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			a.reply(id, nil, "Не удалось открыть изображение.")
			return
		}
		if info.Size() > 10<<20 {
			a.reply(id, nil, "Каждое изображение должно быть не больше 10 МБ.")
			return
		}
		data, err := os.ReadFile(p)
		if err != nil {
			a.reply(id, nil, "Не удалось открыть изображение.")
			return
		}
		png := bytes.HasPrefix(data, []byte{0x89, 0x50, 0x4e, 0x47})
		jpeg := bytes.HasPrefix(data, []byte{0xff, 0xd8, 0xff})
		if !png && !jpeg {
			a.reply(id, nil, "Выберите изображение PNG или JPEG.")
			return
		}
		mime := "image/jpeg"
		if png {
			mime = "image/png"
		}
		images = append(images, map[string]any{"image_base64": base64.StdEncoding.EncodeToString(data), "name": filepath.Base(p), "size": len(data), "mime": mime})
	}
	if !multi {
		a.reply(id, images[0], "")
		return
	}
	a.reply(id, map[string]any{"images": images}, "")
}

func (a *app) saveImage(id int, body map[string]any) {
	image := str(body["image"])
	png := strings.HasPrefix(image, "data:image/png;base64,")
	if len(image) > 15<<20 || (!png && !strings.HasPrefix(image, "data:image/jpeg;base64,")) {
		a.reply(id, nil, "Изображение недоступно.")
		return
	}
	_, encoded, _ := strings.Cut(image, ",")
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		a.reply(id, nil, "Изображение недоступно.")
		return
	}
	name := str(body["name"])
	if name == "" {
		name = "Quantway.png"
	}
	name = filepath.Base(strings.ReplaceAll(name, "/", "\\"))
	path, err := saveFileDialog(a.hwnd, name, png)
	if err != nil || path == "" {
		a.reply(id, map[string]any{}, "")
		return
	}
	if os.WriteFile(path, data, 0o644) != nil {
		a.reply(id, nil, "Не удалось сохранить изображение.")
		return
	}
	a.reply(id, map[string]any{}, "")
}

func str(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case float64:
		return strconv.FormatFloat(s, 'f', -1, 64)
	}
	return ""
}

var _ = windows.Handle(0)
