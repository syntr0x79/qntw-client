package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/syntr0x79/qntw-client/mobile/clienttinc"
)

var deviceLinkToken = regexp.MustCompile(`^/sub/([A-Za-z0-9_-]{16,256})/?$`)
var countryCode = regexp.MustCompile(`^[A-Z]{2}$`)

func publicTincState(state string) (string, bool) {
	switch state {
	case "preparing", "waiting", "connected", "reconnecting", "expired", "failed":
		return state, true
	case "connecting":
		return "waiting", true
	case "revoked":
		return "expired", true
	case "error":
		return "failed", true
	case "stopped":
		return "", false
	default:
		return "failed", true
	}
}

func tincCredentials(link string) (clienttinc.Credentials, error) {
	if len(link) > 8192 {
		return clienttinc.Credentials{}, errors.New("invalid device link")
	}
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Host, "qntw.link") || u.User != nil || u.Fragment != "" || u.EscapedPath() != u.Path {
		return clienttinc.Credentials{}, errors.New("invalid device link")
	}
	match := deviceLinkToken.FindStringSubmatch(u.Path)
	if len(match) != 2 {
		return clienttinc.Credentials{}, errors.New("invalid device link")
	}
	return clienttinc.Credentials{Origin: "https://qntw.link", Token: match[1]}, nil
}

func tincFailure(err error) result {
	out := result{Error: "Не удалось связаться с сервисом подключения. Проверьте сеть и повторите попытку.", Code: "service_unavailable"}
	var apiError clienttinc.APIError
	if errors.As(err, &apiError) {
		out.Status = apiError.Status
		switch apiError.Status {
		case 401:
			out.Code, out.Error = "invalid_device_token", "Ссылка этого компьютера недействительна. Обновите её в кабинете."
		case 402:
			out.Code, out.Error = "subscription_inactive", "Продлите или возобновите подписку, чтобы подключиться."
		case 403:
			out.Code, out.Error = "access_denied", "Доступ к Tinc для этого устройства ограничен. Обратитесь в поддержку."
		default:
			out.Code, out.Error = "service_unavailable", "Сервис подключения временно недоступен. Повторите попытку позже."
		}
	}
	return out
}

func tincCatalog(req request) {
	credentials, err := tincCredentials(req.Link)
	if err != nil {
		_ = json.NewEncoder(responseOut).Encode(result{Status: 400, Code: "invalid_device_link", Error: "Выберите подписку этого компьютера в кабинете Quantway."})
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	catalog, err := clienttinc.NewAPI(credentials, nil).Catalog(ctx)
	if err != nil {
		_ = json.NewEncoder(responseOut).Encode(tincFailure(err))
		os.Exit(1)
	}
	_ = json.NewEncoder(responseOut).Encode(struct {
		Data any `json:"data"`
	}{Data: catalog})
}
