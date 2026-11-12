package topology

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/url"
	"strconv"

	"github.com/google/uuid"
)

var ErrBadVLESSLink = errors.New("bad vless link")

// vlessLinkParams — параметры, которые BuildLink кладёт в ссылку как есть
// (помимо type/security/encryption и xhttp-половины).
var vlessLinkParams = []string{"sni", "pbk", "sid", "fp", "alpn", "flow", "serviceName"}

// ParseVLESSLink читает vless:// обратно: вход с клиентской половиной
// параметров, id и метку — обратная сторона BuildLink. Нужна тесту
// обратимости CDN-профиля и e2e-клиенту: ссылку из подписки разбирает код
// магазина, а не представление теста о ней.
//
// У xhttp extra раскладывается в Params ключ в ключ, а host/path/mode берутся
// из внешних параметров — так их склеивает и xray (SplitHTTPConfig.Build).
// Серверных ключей в ссылке нет и быть не может (xhttpServerKeys, listenPort).
func ParseVLESSLink(uri string) (Inbound, uuid.UUID, string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return Inbound{}, uuid.Nil, "", fmt.Errorf("%w: %v", ErrBadVLESSLink, err)
	}
	if u.Scheme != "vless" {
		return Inbound{}, uuid.Nil, "", fmt.Errorf("%w: scheme %q", ErrBadVLESSLink, u.Scheme)
	}
	id, err := uuid.Parse(u.User.Username())
	if err != nil {
		return Inbound{}, uuid.Nil, "", fmt.Errorf("%w: id is not a uuid: %v", ErrBadVLESSLink, err)
	}
	host, portStr, err := net.SplitHostPort(u.Host)
	if err != nil {
		return Inbound{}, uuid.Nil, "", fmt.Errorf("%w: %v", ErrBadVLESSLink, err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return Inbound{}, uuid.Nil, "", fmt.Errorf("%w: port %q: %v", ErrBadVLESSLink, portStr, err)
	}

	q := u.Query()
	params := map[string]any{}
	for _, k := range vlessLinkParams {
		if v := q.Get(k); v != "" {
			params[k] = v
		}
	}
	kind := q.Get("type")
	switch kind {
	case "xhttp":
		if blob := q.Get("extra"); blob != "" {
			extra := map[string]any{}
			if err := json.Unmarshal([]byte(blob), &extra); err != nil {
				return Inbound{}, uuid.Nil, "", fmt.Errorf("%w: extra is not a JSON object: %v", ErrBadVLESSLink, err)
			}
			maps.Copy(params, extra)
		}
		for _, k := range xhttpOuterKeys {
			if v := q.Get(k); v != "" {
				params[k] = v
			}
		}
	case "ws", "httpupgrade":
		for _, k := range []string{"path", "host"} {
			if v := q.Get(k); v != "" {
				params[k] = v
			}
		}
	}
	return Inbound{
		Tag:       u.Fragment,
		Protocol:  u.Scheme,
		Host:      host,
		Port:      port,
		Transport: Transport{Kind: kind, Sec: q.Get("security"), Params: params},
	}, id, u.Fragment, nil
}
