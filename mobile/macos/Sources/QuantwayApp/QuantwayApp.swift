import AppKit
import WebKit
import CoreImage
import UniformTypeIdentifiers
import QuantwayCore

@main enum QuantwayApp {
    static func main() {
        let app = NSApplication.shared
        let delegate = AppDelegate()
        app.delegate = delegate
        app.setActivationPolicy(.regular)
        app.run()
    }
}

final class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate, WKScriptMessageHandlerWithReply, WKNavigationDelegate {
    var window: NSWindow!
    var web: WKWebView!
    var vpn: Process?
    var vpnInput: FileHandle?
    var vpnMode = ""
    var cookie = ""
    var terminating = false
    var authEpoch = 0
    var loginProcess: Process?
    var probeProcess: Process?
    var statusItem: NSStatusItem!
    var trayStatus: NSMenuItem!
    var quitApproved = false

    func applicationDidFinishLaunching(_ notification: Notification) {
        let configuration = WKWebViewConfiguration()
        configuration.websiteDataStore = .nonPersistent()
        configuration.userContentController.addScriptMessageHandler(self, contentWorld: .page, name: "quantway")
        web = WKWebView(frame: .zero, configuration: configuration)
        web.navigationDelegate = self
        web.setValue(false, forKey: "drawsBackground")
        window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 1100, height: 700), styleMask: [.titled, .closable, .miniaturizable, .resizable, .fullSizeContentView], backing: .buffered, defer: false)
        window.title = "Quantway"
        window.titleVisibility = .hidden
        window.titlebarAppearsTransparent = true
        window.standardWindowButton(.closeButton)?.isHidden = true
        window.standardWindowButton(.miniaturizeButton)?.isHidden = true
        window.standardWindowButton(.zoomButton)?.isHidden = true
        window.minSize = NSSize(width: 960, height: 640)
        window.isMovableByWindowBackground = true
        window.backgroundColor = NSColor(srgbRed: 13/255, green: 20/255, blue: 27/255, alpha: 1)
        window.contentView = web
        window.delegate = self
        window.center()
        let menu = NSMenu()
        let item = NSMenuItem(); menu.addItem(item)
        let appMenu = NSMenu(); item.submenu = appMenu
        appMenu.addItem(withTitle: "О программе Quantway", action: #selector(about), keyEquivalent: "")
        appMenu.addItem(.separator())
        appMenu.addItem(withTitle: "Завершить Quantway", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        let editItem = NSMenuItem(); menu.addItem(editItem); let edit = NSMenu(title: "Правка"); editItem.submenu = edit
        for (title, action, key) in [("Копировать", "copy:", "c"), ("Вставить", "paste:", "v"), ("Вырезать", "cut:", "x"), ("Выбрать всё", "selectAll:", "a")] { edit.addItem(withTitle: title, action: Selector(action), keyEquivalent: key) }
        let accountItem = NSMenuItem(); menu.addItem(accountItem); let account = NSMenu(title: "Кабинет"); accountItem.submenu = account
        for (title, key) in [("Использовать подписку Quantway", "quantway"), ("Устройства", "devices"), ("Счета и оплаты", "invoices"), ("История подписки", "history"), ("Поддержка", "support"), ("Промокод", "promo"), ("Пауза подписки", "pause"), ("Telegram", "telegram"), ("Присоединить покупку", "purchase"), ("Код входа", "token"), ("Выйти из аккаунта", "logout")] {
            let entry = account.addItem(withTitle: title, action: #selector(accountAction(_:)), keyEquivalent: ""); entry.representedObject = key
        }
        let helpItem = NSMenuItem(); menu.addItem(helpItem); let help = NSMenu(title: "Справка"); helpItem.submenu = help
        let supportEntry = help.addItem(withTitle: "Написать в поддержку", action: #selector(accountAction(_:)), keyEquivalent: "")
        supportEntry.representedObject = "support"; supportEntry.target = self
        NSApplication.shared.mainMenu = menu
        statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
        statusItem.button?.image = NSImage(systemSymbolName: "shield", accessibilityDescription: "Quantway")
        let tray = NSMenu()
        trayStatus = tray.addItem(withTitle: "VPN выключен", action: nil, keyEquivalent: "")
        tray.addItem(withTitle: "Открыть Quantway", action: #selector(showWindow), keyEquivalent: "")
        tray.addItem(withTitle: "Отключить VPN", action: #selector(trayDisconnect), keyEquivalent: "")
        tray.addItem(.separator())
        tray.addItem(withTitle: "Завершить Quantway", action: #selector(NSApplication.terminate(_:)), keyEquivalent: "q")
        for entry in tray.items where entry.action != nil { entry.target = self }
        // Termination is still routed through applicationShouldTerminate and graceful cleanup.
        tray.items.last?.target = NSApp
        statusItem.menu = tray
        let packaged = Bundle.main.resourceURL!.appendingPathComponent("QuantwayMac_QuantwayApp.bundle/UI")
        let root = FileManager.default.fileExists(atPath: packaged.appendingPathComponent("index.html").path) ? packaged : Bundle.module.url(forResource: "UI", withExtension: nil)!
        web.loadFileURL(root.appendingPathComponent("index.html"), allowingReadAccessTo: root)
        window.makeKeyAndOrderFront(nil)
        NSApp.activate(ignoringOtherApps: true)
    }
    @objc func showWindow() { window.makeKeyAndOrderFront(nil); NSApp.activate(ignoringOtherApps: true) }
    @objc func trayDisconnect() { stopVPN() }
    func applicationShouldHandleReopen(_ sender: NSApplication, hasVisibleWindows flag: Bool) -> Bool { showWindow(); return true }
    func applicationDidBecomeActive(_ notification: Notification) { web?.evaluateJavaScript("window.appBecameActive?.()", completionHandler: nil) }
    @objc func about() { NSApp.orderFrontStandardAboutPanel(options: [.applicationName: "Quantway", .applicationVersion: (Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "0.2.1") + " · macOS", .credits: NSAttributedString(string: "Интерфейс по концепту Quantway. Tinc, Xray и AmneziaWG.")]) }
    @objc func accountAction(_ sender: NSMenuItem) { if let key = sender.representedObject as? String { send("window.accountAction", key) } }
    func send(_ function: String, _ value: Any) {
        guard let bytes = try? JSONSerialization.data(withJSONObject: value, options: [.fragmentsAllowed]), let json = String(data: bytes, encoding: .utf8) else { return }
        web.evaluateJavaScript("\(function)(\(json))", completionHandler: nil)
    }
    func helperURL(_ name: String = "quantway-helper") -> URL { Bundle.main.bundleURL.appendingPathComponent("Contents/Helpers/\(name)") }
    func run(_ action: String, _ body: [String: Any], completion: @escaping ([String: Any]) -> Void) {
        let process = Process(); process.executableURL = helperURL(); process.arguments = ["--json", "--action", action] + (action == "probe" ? ["--progress"] : [])
        let input = Pipe(), output = Pipe(); process.standardInput = input; process.standardOutput = output; process.standardError = FileHandle.nullDevice
        guard let data = action == "profiles" ? (body["link"] as? String)?.data(using: .utf8) : (try? JSONSerialization.data(withJSONObject: body)) else { completion(["error": "Некорректный запрос."]); return }
        if action == "login" { loginProcess = process }
        if action == "probe" { probeProcess = process }
        let epoch = authEpoch
        // Read concurrently so support attachments cannot fill the pipe.
        DispatchQueue.global().async {
            do {
                if DispatchQueue.main.sync(execute: { (action == "login" && self.authEpoch != epoch) || (action == "probe" && self.probeProcess !== process) }) {
                    DispatchQueue.main.async { completion(["error": "Вход отменён."]) }; return
                }
                try process.run(); input.fileHandleForWriting.write(data + Data([10])); try? input.fileHandleForWriting.close()
                let timer = DispatchWorkItem { if process.isRunning { process.terminate() } }
                DispatchQueue.global().asyncAfter(deadline: .now() + 180, execute: timer)
                var response: [String: Any]?
                if action == "probe" {
                    var buffer = Data()
                    while true {
                        let chunk = output.fileHandleForReading.availableData; if chunk.isEmpty { break }; buffer.append(chunk)
                        while let end = buffer.firstIndex(of: 10) {
                            let line = Data(buffer[..<end]); buffer.removeSubrange(...end)
                            if let event = (try? JSONSerialization.jsonObject(with: line)) as? [String: Any] {
                                if event["type"] as? String == "progress" { DispatchQueue.main.async { if self.probeProcess === process { self.send("window.probeEvent", event) } } }
                                else { response = event }
                            }
                        }
                    }
                } else {
                    let result = output.fileHandleForReading.readDataToEndOfFile()
                    response = (try? JSONSerialization.jsonObject(with: result)) as? [String: Any]
                }
                process.waitUntilExit(); timer.cancel()
                let final = response ?? ["error": "Не удалось получить ответ. Проверьте подключение к сети."]
                DispatchQueue.main.async {
                    if self.loginProcess === process { self.loginProcess = nil }
                    if self.probeProcess === process { self.probeProcess = nil }
                    completion(final)
                }
            } catch { DispatchQueue.main.async { completion(["error": "Сетевой компонент отсутствует или недоступен."]) } }
        }
    }
    func userContentController(_ userContentController: WKUserContentController, didReceive message: WKScriptMessage, replyHandler: @escaping (Any?, String?) -> Void) {
        guard message.frameInfo.isMainFrame, message.frameInfo.request.url?.isFileURL == true,
              let body = message.body as? [String: Any], let action = body["action"] as? String else { replyHandler(nil, "Недопустимый запрос."); return }
        var payload = body["body"] as? [String: Any] ?? [:]
        switch action {
        case "minimize": window.miniaturize(nil); replyHandler([:], nil)
        case "close", "quit": quitApproved = true; NSApp.terminate(nil); replyHandler([:], nil)
        case "hide-to-tray":
            if payload["remember"] as? Bool == true { UserDefaults.standard.set(true, forKey: "closeToTray") }
            window.orderOut(nil); replyHandler([:], nil)
        case "cancel-login": authEpoch += 1; if loginProcess?.isRunning == true { loginProcess?.terminate() }; loginProcess = nil; replyHandler([:], nil)
        case "cancel-probe": if probeProcess?.isRunning == true { probeProcess?.terminate() }; probeProcess = nil; replyHandler([:], nil)
        case "retry-storage":
            do { guard !cookie.isEmpty else { replyHandler(nil, "Войдите снова."); return }; try CredentialStore.write(cookie, account: "session"); UserDefaults.standard.set(false, forKey: "autologinDisabled"); replyHandler([:], nil) }
            catch { replyHandler(nil, "Связка ключей по-прежнему недоступна. Разрешите доступ в системном окне macOS.") }
        case "bootstrap":
            cookie = UserDefaults.standard.bool(forKey: "autologinDisabled") ? "" : (CredentialStore.read("session") ?? "")
            let providers = CredentialStore.read("providers") ?? "[]"
            let parsed = (try? JSONSerialization.jsonObject(with: Data(providers.utf8))) as? [[String: Any]] ?? []
            replyHandler(["hasSession": !cookie.isEmpty, "providers": parsed, "legacyLink": CredentialStore.read("subscription") ?? "", "closeToTray": UserDefaults.standard.bool(forKey: "closeToTray")], nil)
        case "save-providers":
            do { let data = try JSONSerialization.data(withJSONObject: payload["providers"] ?? []); try CredentialStore.write(String(decoding: data, as: UTF8.self), account: "providers"); replyHandler([:], nil) }
            catch { replyHandler(nil, "Не удалось сохранить подписки в связке ключей.") }
        case "open":
            if let value = payload["url"] as? String, let url = URL(string: value), ["https", "tg"].contains(url.scheme?.lowercased() ?? "") { NSWorkspace.shared.open(url) }
            replyHandler([:], nil)
        case "qr":
            if let text = payload["text"] as? String, text.count <= 8192,
               let filter = CIFilter(name: "CIQRCodeGenerator") {
                filter.setValue(Data(text.utf8), forKey: "inputMessage")
                if let output = filter.outputImage?.transformed(by: CGAffineTransform(scaleX: 8, y: 8)),
                   let cg = CIContext().createCGImage(output, from: output.extent),
                   let data = NSBitmapImageRep(cgImage: cg).representation(using: .png, properties: [:]) {
                    replyHandler(["image": "data:image/png;base64," + data.base64EncodedString()], nil); return
                }
            }
            replyHandler(nil, "Не удалось создать QR-код.")
        case "pick-images", "pick-image":
            let panel = NSOpenPanel(); panel.allowedContentTypes = [.png, .jpeg]; panel.allowsMultipleSelection = action == "pick-images"
            let limit = max(1, min(5, payload["limit"] as? Int ?? 1))
            panel.beginSheetModal(for: window) { result in
                guard result == .OK else { replyHandler([:], nil); return }
                guard panel.urls.count <= limit else { replyHandler(nil, "Можно прикрепить не больше пяти изображений."); return }
                do {
                    var images: [[String: Any]] = []
                    for url in panel.urls {
                        let values = try url.resourceValues(forKeys: [.fileSizeKey])
                        guard (values.fileSize ?? Int.max) <= 10 * 1024 * 1024 else { replyHandler(nil, "Каждое изображение должно быть не больше 10 МБ."); return }
                        let data = try Data(contentsOf: url, options: .mappedIfSafe)
                        let png = data.starts(with: [0x89, 0x50, 0x4e, 0x47]), jpeg = data.starts(with: [0xff, 0xd8, 0xff])
                        guard png || jpeg else { replyHandler(nil, "Выберите изображение PNG или JPEG."); return }
                        images.append(["image_base64": data.base64EncodedString(), "name": url.lastPathComponent, "size": data.count, "mime": png ? "image/png" : "image/jpeg"])
                    }
                    replyHandler(action == "pick-image" ? (images.first ?? [:]) : ["images": images], nil)
                } catch { replyHandler(nil, "Не удалось открыть изображение.") }
            }
        case "save-image":
            guard let image = payload["image"] as? String, image.count <= 15 * 1024 * 1024,
                  image.hasPrefix("data:image/png;base64,") || image.hasPrefix("data:image/jpeg;base64,"),
                  let encoded = image.split(separator: ",", maxSplits: 1).last, let data = Data(base64Encoded: String(encoded)) else { replyHandler(nil, "Изображение недоступно."); return }
            let panel = NSSavePanel(); panel.nameFieldStringValue = (payload["name"] as? String ?? "Quantway.png").components(separatedBy: "/").last ?? "Quantway.png"
            panel.allowedContentTypes = image.hasPrefix("data:image/png") ? [.png] : [.jpeg]
            panel.beginSheetModal(for: window) { result in
                guard result == .OK, let url = panel.url else { replyHandler([:], nil); return }
                do { try data.write(to: url, options: .atomic); replyHandler([:], nil) } catch { replyHandler(nil, "Не удалось сохранить изображение.") }
            }
        case "copy": NSPasteboard.general.clearContents(); NSPasteboard.general.setString(payload["text"] as? String ?? "", forType: .string); replyHandler([:], nil)
        case "start":
            guard vpn == nil else { replyHandler(nil, "Сначала отключите текущее подключение."); return }
            startVPN(payload); replyHandler([:], nil)
        case "stop": stopVPN(); replyHandler([:], nil)
        case "country":
            guard vpn != nil, vpnMode == "tinc", let input = vpnInput,
                  let country = payload["country"] as? String,
                  country.range(of: "^[A-Z]{2}$", options: .regularExpression) != nil else {
                replyHandler(nil, "Смена страны доступна только в активном Tinc-подключении."); return
            }
            do {
                let data = try JSONSerialization.data(withJSONObject: ["action": "country", "country": country])
                try input.write(contentsOf: data + Data([10]))
                replyHandler(["accepted": true], nil)
            } catch { replyHandler(nil, "Не удалось передать команду смены страны.") }
        case "logout":
            stopVPN(); authEpoch += 1; let previous = cookie; cookie = ""; UserDefaults.standard.set(true, forKey: "autologinDisabled"); CredentialStore.delete("session")
            run("cabinet", ["cookie": previous, "method": "POST", "path": "logout"]) { _ in }; replyHandler([:], nil)
        case "login", "restore-session", "cabinet", "profiles", "probe", "tinc-catalog", "account-refresh", "replace-device", "device-link", "plans", "invoice", "invoice-status":
            if action == "restore-session" || action == "cabinet" || ["account-refresh", "replace-device", "device-link", "plans", "invoice", "invoice-status"].contains(action) { payload["cookie"] = cookie }
            if action == "profiles" {
                // Existing CLI profiles protocol reads the raw URL, not a JSON object.
                runProfiles(payload["link"] as? String ?? "", replyHandler); return
            }
            if action == "login" { authEpoch += 1 }
            let epoch = authEpoch
            run(action, payload) { response in
                guard epoch == self.authEpoch else { replyHandler(["error": "Вход отменён."], nil); return }
                var safe = response
                if var session = response["session"] as? [String: Any] {
                    if let value = session.removeValue(forKey: "cookie") as? String {
                        self.cookie = value
                        do { try CredentialStore.write(value, account: "session"); UserDefaults.standard.set(false, forKey: "autologinDisabled") } catch { safe["storageWarning"] = "Вход сохранён только до закрытия приложения: связка ключей недоступна." }
                    }
                    safe["session"] = session
                }
                if action == "cabinet", payload["path"] as? String == "token", var data = response["data"] as? [String: Any] {
                    if let value = data.removeValue(forKey: "session_cookie") as? String {
                        self.cookie = value; self.authEpoch += 1
                        do { try CredentialStore.write(value, account: "session") } catch { safe["storageWarning"] = "Не удалось сохранить новый вход в связке ключей." }
                    } else { self.cookie = ""; CredentialStore.delete("session") }
                    safe["data"] = data
                }
                if action == "restore-session", (response["status"] as? Int) == 401 { self.cookie = ""; CredentialStore.delete("session") }
                replyHandler(safe, nil)
            }
        default: replyHandler(nil, "Неизвестная операция.")
        }
    }
    func runProfiles(_ link: String, _ reply: @escaping (Any?, String?) -> Void) {
        run("profiles", ["link": link]) { response in reply(response, nil) }
    }
    func startVPN(_ body: [String: Any]) {
        let process = Process(); process.executableURL = helperURL(); process.arguments = ["--json", "--action", (body["mode"] as? String) == "tinc" ? "tinc-connect" : "connect"]
        let input = Pipe(), output = Pipe(); process.standardInput = input; process.standardOutput = output; process.standardError = FileHandle.nullDevice
        do {
            let data = try JSONSerialization.data(withJSONObject: body)
            vpn = process; vpnInput = input.fileHandleForWriting; vpnMode = (body["mode"] as? String) ?? "vpn"
            try process.run(); input.fileHandleForWriting.write(data + Data([10]))
            DispatchQueue.global().async {
                var buffer = Data()
                while true {
                    let chunk = output.fileHandleForReading.availableData; if chunk.isEmpty { break }; buffer.append(chunk)
                    while let end = buffer.firstIndex(of: 10) {
                        let line = Data(buffer[..<end]); buffer.removeSubrange(...end)
                        if let event = try? JSONSerialization.jsonObject(with: line) {
                            // The helper emits public lifecycle/telemetry only; requests,
                            // subscription URLs, cookies and keys are never recorded.
                            let dir = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0].appendingPathComponent("Quantway/Diagnostics")
                            try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
                            let log = dir.appendingPathComponent("connection.jsonl")
                            if !FileManager.default.fileExists(atPath: log.path) { FileManager.default.createFile(atPath: log.path, contents: nil, attributes: [.posixPermissions: 0o600]) }
                            if let file = try? FileHandle(forWritingTo: log) { try? file.seekToEnd(); try? file.write(contentsOf: line + Data([10])); try? file.close() }
                            DispatchQueue.main.async {
                                if let status = (event as? [String: Any])?["status"] as? String {
                                    self.trayStatus.title = ["connected": "VPN подключён", "connecting": "Подключение…", "stopping": "Отключение…", "failed": "Ошибка подключения"][status] ?? "VPN: настройка соединения"
                                    self.statusItem.button?.image = NSImage(systemSymbolName: status == "connected" ? "shield.fill" : "shield", accessibilityDescription: self.trayStatus.title)
                                }
                                self.send("window.sessionEvent", event)
                            }
                        }
                    }
                }
                process.waitUntilExit()
                DispatchQueue.main.async {
                    self.vpn = nil; self.vpnInput = nil; self.vpnMode = ""
                    self.trayStatus.title = "VPN выключен"
                    self.statusItem.button?.image = NSImage(systemSymbolName: "shield", accessibilityDescription: "Quantway")
                    self.send("window.sessionEvent", ["status": "ended", "exitCode": process.terminationStatus])
                    if self.terminating { NSApp.reply(toApplicationShouldTerminate: true) }
                }
            }
        } catch {
            vpn = nil; vpnInput = nil; vpnMode = ""
            send("window.sessionEvent", ["status": "failed", "error": "Не удалось запустить сетевой компонент."])
            send("window.sessionEvent", ["status": "ended", "exitCode": 1])
        }
    }
    func stopVPN() { vpnInput?.write(Data("stop\n".utf8)); try? vpnInput?.close(); vpnInput = nil }
    func applicationShouldTerminate(_ sender: NSApplication) -> NSApplication.TerminateReply {
        if vpn != nil && !quitApproved { send("window.lifecycleAction", "quit"); return .terminateCancel }
        if vpn != nil { terminating = true; stopVPN(); return .terminateLater }; return .terminateNow
    }
    func windowShouldClose(_ sender: NSWindow) -> Bool {
        if vpn != nil && UserDefaults.standard.bool(forKey: "closeToTray") { window.orderOut(nil) }
        else { send("window.lifecycleAction", "close") }
        return false
    }
    func webView(_ webView: WKWebView, decidePolicyFor navigationAction: WKNavigationAction, decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
        decisionHandler(navigationAction.request.url?.isFileURL == true ? .allow : .cancel)
    }
}
