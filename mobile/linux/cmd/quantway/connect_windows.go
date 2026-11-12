package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/signal"
	"sync"

	"github.com/syntr0x79/qntw-client/mobile/clienttinc"
	"github.com/syntr0x79/qntw-client/mobile/windows/desktopvpn"
	"github.com/syntr0x79/qntw-client/mobile/windows/elevation"
)

func windowsConnect(req request, reader *bufio.Reader, mode string) {
	start := elevation.Packet{Type: "start", Mode: mode}
	if mode == "tinc" {
		creds, e := tincCredentials(req.Link)
		if e != nil || !countryCode.MatchString(req.Country) {
			_ = json.NewEncoder(responseOut).Encode(map[string]string{"status": "failed", "error": "Выберите подписку Quantway и страну подключения."})
			return
		}
		start.Origin, start.Token, start.Country = creds.Origin, creds.Token, req.Country
	} else {
		start.Job, start.ControlURL = req.Job, req.ControlURL
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	var outputMu sync.Mutex
	write := func(value any) {
		outputMu.Lock()
		defer outputMu.Unlock()
		if json.NewEncoder(responseOut).Encode(value) != nil {
			cancel()
		}
	}
	e := desktopvpn.Run(ctx, start, reader, func(p elevation.Packet) {
		switch p.Type {
		case "failed":
			write(map[string]string{"status": "failed", "code": p.Code, "error": windowsFailure(p.Code)})
		case "status":
			if p.Status != nil {
				s := clienttinc.CloneStatus(*p.Status)
				state, include := publicTincState(s.State)
				if !include {
					return
				}
				s.State = state
				write(struct {
					clienttinc.Status
					StatusName string `json:"status"`
				}{s, state})
			}
		}
	})
	if e != nil && ctx.Err() == nil {
		write(map[string]string{"status": "failed", "code": "windows_setup_failed", "error": "Не удалось подключиться. Проверьте запрос прав администратора и повторите попытку."})
	}
}
func windowsFailure(code string) string {
	switch code {
	case "vpn_busy":
		return "Уже работает другое VPN-подключение. Отключите его перед новым подключением."
	case "device_authorization_failed":
		return "Не удалось подтвердить доступ устройства. Проверьте подписку и подключение к сети."
	case "native_missing":
		return "Не найдены компоненты VPN. Переустановите Quantway."
	case "cleanup_failed":
		return "Не удалось полностью восстановить сетевые настройки. Перезапустите приложение перед новым подключением."
	default:
		return "Не удалось подключиться. Проверьте сеть, разрешение администратора и повторите попытку."
	}
}
func connect(req request, r *bufio.Reader)     { windowsConnect(req, r, "vpn") }
func tincConnect(req request, r *bufio.Reader) { windowsConnect(req, r, "tinc") }
