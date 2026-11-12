//go:build linux && !android

package tincvpn

import (
	"errors"
	"net"
	"os/exec"
	"time"

	"github.com/syntr0x79/qntw-client/mobile/clienttinc"
)

// Failure contains only an allowlisted public reason, never native stderr,
// a request, credentials, an arbitrary server message or a private pathname.
type Failure struct{ Code string }

func (e Failure) Error() string { return "tinc failure: " + e.Code }

func KnownFailure(code string) bool {
	switch code {
	case "run_as_user", "helper_missing", "helper_unsafe", "native_missing", "pkexec_missing", "pkexec_unsafe",
		"authorization_cancelled", "authorization_failed", "helper_start_failed", "helper_protocol_failed", "helper_initialization_failed",
		"device_authorization_failed", "device_state_unavailable", "vpn_busy", "vpn_lock_failed", "network_setup_failed", "cleanup_failed",
		"identity_conflict", "identity_failed", "invalid_device_token", "subscription_inactive", "access_denied", "dns_unavailable", "network_tools_missing", "tun_unavailable", "route_conflict":
		return true
	}
	return false
}

func failure(code string) error {
	if !KnownFailure(code) {
		code = "helper_protocol_failed"
	}
	return Failure{Code: code}
}

func FailureCode(err error) string {
	var f Failure
	if errors.As(err, &f) && KnownFailure(f.Code) {
		return f.Code
	}
	var api clienttinc.APIError
	if errors.As(err, &api) {
		switch api.Status {
		case 401:
			return "invalid_device_token"
		case 402:
			return "subscription_inactive"
		case 403, 410:
			return "access_denied"
		case 409:
			return "identity_conflict"
		}
	}
	var runtime clienttinc.RuntimeError
	if errors.As(err, &runtime) && KnownFailure(runtime.Code) {
		return runtime.Code
	}
	return "network_setup_failed"
}

func FailureMessage(code string) string {
	messages := map[string]string{
		"run_as_user":                  "Запустите Quantway обычным пользователем, без sudo.",
		"helper_missing":               "Сетевой помощник Tinc не установлен. Запустите setup-vm.sh из этого архива.",
		"helper_unsafe":                "Неверные права сетевого помощника Tinc. Повторите установку через setup-vm.sh.",
		"native_missing":               "Компоненты Tinc не установлены. Повторите установку через setup-vm.sh.",
		"pkexec_missing":               "Не установлен PolicyKit (pkexec). Установите его и повторите подключение.",
		"pkexec_unsafe":                "Неверные права PolicyKit (pkexec). Проверьте установку системного пакета.",
		"authorization_cancelled":      "Запрос разрешения администратора отменён.",
		"authorization_failed":         "Не удалось получить разрешение администратора. Проверьте PolicyKit и системный запрос.",
		"helper_start_failed":          "Не удалось запустить сетевой помощник Tinc. Повторите установку.",
		"helper_protocol_failed":       "Нарушена связь с сетевым помощником Tinc. Переустановите помощники из текущего архива.",
		"helper_initialization_failed": "Сетевой помощник Tinc не смог запуститься. Повторите установку.",
		"device_authorization_failed":  "Не удалось проверить доступ этого устройства к Tinc. Повторите попытку.",
		"device_state_unavailable":     "Не удалось открыть защищённые данные Tinc. Обратитесь в поддержку.",
		"vpn_busy":                     "Другое VPN-подключение ещё работает. Сначала отключите его.",
		"vpn_lock_failed":              "Не удалось занять сетевой помощник. Закройте другое VPN-подключение и повторите попытку.",
		"network_setup_failed":         "Не удалось настроить сеть для Tinc. Проверьте установку и поддержку VPN в системе.",
		"cleanup_failed":               "Не удалось полностью восстановить сетевые настройки. Новое подключение не запущено.",
		"identity_conflict":            "Ключ Tinc этого устройства не совпадает с сохранённым на сервере. Обратитесь в поддержку.",
		"identity_failed":              "Не удалось открыть или создать ключ Tinc. Обратитесь в поддержку.",
		"invalid_device_token":         "Ссылка этого компьютера недействительна. Обновите её в кабинете.",
		"subscription_inactive":        "Продлите или возобновите подписку, чтобы подключиться.",
		"access_denied":                "Доступ к Tinc для этого устройства ограничен. Обратитесь в поддержку.",
		"dns_unavailable":              "Не удалось настроить DNS для Tinc. Проверьте работу systemd-resolved.",
		"network_tools_missing":        "Не найдены iproute2 или nftables. Установите их и повторите подключение.",
		"tun_unavailable":              "Не удалось открыть VPN-интерфейс. Проверьте поддержку TUN в системе.",
		"route_conflict":               "Обнаружены сетевые настройки другого VPN. Отключите его и повторите попытку.",
	}
	if message := messages[code]; message != "" {
		return message
	}
	return "Не удалось подключиться в режиме Tinc. Повторите попытку."
}

// pkexec documents 126 as a dismissed dialog and 127 as authorization failure
// or inability to obtain authorization. Do not inspect or expose its stderr.
func launchFailure(done <-chan error) error {
	select {
	case err := <-done:
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			switch exit.ExitCode() {
			case 126:
				return failure("authorization_cancelled")
			case 127:
				return failure("authorization_failed")
			}
		}
	case <-time.After(300 * time.Millisecond):
	}
	return failure("helper_initialization_failed")
}

func startupPacketFailure(p packet) error {
	if p.Type == "error" && p.ID == 0 && p.Status == nil && p.Origin == "" && p.Token == "" && p.Country == "" && !p.WantMesh && KnownFailure(p.Code) {
		return failure(p.Code)
	}
	return nil
}

// A helper can reject its local installation before reading the start packet.
// On SOCK_SEQPACKET, closing with unread input may surface ECONNRESET before
// an already queued error reply. Read that bounded reply before discarding it.
func queuedStartupFailure(conn *net.UnixConn) error {
	_ = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	p, err := receive(conn)
	if err == nil {
		return startupPacketFailure(p)
	}
	return nil
}
