package xrayfetch

import (
	"errors"
	"fmt"

	"google.golang.org/protobuf/proto"

	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/core"
	hyproxy "github.com/xtls/xray-core/proxy/hysteria"
	"github.com/xtls/xray-core/proxy/shadowsocks"
	"github.com/xtls/xray-core/proxy/trojan"
	vlessout "github.com/xtls/xray-core/proxy/vless/outbound"
	vmessout "github.com/xtls/xray-core/proxy/vmess/outbound"
)

// ErrNoServer — у исходящего не найден адрес сервера: протокол незнаком
// или настройки не читаются. Агент маяка оборачивает его своим
// beaconagent.ErrUnbindable, узел парка отдаёт как есть («не проверяемо»).
var ErrNoServer = errors.New("xrayfetch: адрес сервера исходящего не найден")

// ServerEndpoint — адрес сервера в настройках протокола исходящего: у всех
// клиентских протоколов xray, которые собирает магазин, он один. Пакеты
// протоколов здесь нужны ради типов их настроек (их init заодно
// регистрирует реализации, которые магазин и агенты подключают и так).
func ServerEndpoint(msg proto.Message) (*protocol.ServerEndpoint, error) {
	switch c := msg.(type) {
	case *vlessout.Config:
		return c.GetVnext(), nil
	case *vmessout.Config:
		return c.GetReceiver(), nil
	case *trojan.ClientConfig:
		return c.GetServer(), nil
	case *shadowsocks.ClientConfig:
		return c.GetServer(), nil
	case *hyproxy.ClientConfig:
		return c.GetServer(), nil
	}
	return nil, fmt.Errorf("%w: протокол %T незнаком", ErrNoServer, msg)
}

// ServerHost — адрес сервера (входа) исходящего: имя или IP, как его собрал
// магазин.
func ServerHost(out *core.OutboundHandlerConfig) (string, error) {
	// GetInstance у пустого TypedMessage падает (nil-разыменование), а пустое
	// исходящее — не повод ронять агент.
	if out.GetProxySettings() == nil {
		return "", fmt.Errorf("%w: у исходящего нет настроек протокола", ErrNoServer)
	}
	msg, err := out.GetProxySettings().GetInstance()
	if err != nil {
		return "", fmt.Errorf("%w: настройки протокола: %w", ErrNoServer, err)
	}
	ep, err := ServerEndpoint(msg)
	if err != nil {
		return "", err
	}
	a := ep.GetAddress().AsAddress()
	if a == nil {
		return "", fmt.Errorf("%w: у исходящего нет адреса сервера", ErrNoServer)
	}
	return a.String(), nil
}
