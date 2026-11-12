// Package xrayfetch — GET через одно готовое исходящее xray: общий код
// проверки магазина (internal/probe) и агента маяка (internal/beaconagent).
//
// Пакет нарочно без зависимостей магазина (ни store, ни pgx, ни topology),
// чтобы бинарь агента маяка не тянул базу (решение R12). Транспорты сюда
// не подключаются: их регистрирует тот, кто собирает исходящее (probe
// импортирует свои, агенты — distro/all). Исключение — пакеты клиентских
// протоколов в server.go: они нужны ради типов настроек (адрес сервера), и
// их init заодно регистрирует реализации, которые все вызывающие
// подключают и так.
package xrayfetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/xtls/xray-core/app/dispatcher"
	"github.com/xtls/xray-core/app/proxyman"

	// xray-core resolves a configuration message to an implementation
	// through a registry each implementation package fills in from its own
	// init(). app/proxyman above supplies only the Inbound/OutboundConfig
	// messages; the handlers that claim them live in these two packages,
	// and without them core.New fails outright with "common:
	// *proxyman.InboundConfig is not registered" — for every probe of every
	// route, with an error naming a protobuf type rather than anything an
	// operator could act on.
	_ "github.com/xtls/xray-core/app/proxyman/inbound"
	_ "github.com/xtls/xray-core/app/proxyman/outbound"

	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/core"
)

// ErrInstance — экземпляр xray с этим исходящим не собрался или не
// запустился: исходящее не годится этому бинарю (не зарегистрирован
// протокол, транспорт), до маршрута дело не дошло.
var ErrInstance = errors.New("xrayfetch: экземпляр xray не поднялся")

// HTTPStatusError identifies a rejected control response without retaining its URL.
type HTTPStatusError struct {
	StatusCode int
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("xrayfetch: control request returned HTTP %d", e.StatusCode)
}

func (e *HTTPStatusError) HTTPStatusCode() int { return e.StatusCode }

// NewInstance собирает (не запуская) экземпляр xray ровно с одним
// исходящим out — тот же, каким ходит FetchThrough.
func NewInstance(out *core.OutboundHandlerConfig) (*core.Instance, error) {
	return NewInstanceWithContext(context.Background(), out)
}

// NewInstanceWithContext retains per-instance socket ownership and cancellation.
func NewInstanceWithContext(ctx context.Context, out *core.OutboundHandlerConfig) (*core.Instance, error) {
	inst, err := core.NewWithContext(ctx, &core.Config{
		App: []*serial.TypedMessage{
			serial.ToTypedMessage(&dispatcher.Config{}),
			serial.ToTypedMessage(&proxyman.InboundConfig{}),
			serial.ToTypedMessage(&proxyman.OutboundConfig{}),
		},
		Outbound: []*core.OutboundHandlerConfig{out},
	})
	if err != nil {
		return nil, fmt.Errorf("%w: build probe instance: %w", ErrInstance, err)
	}
	return inst, nil
}

type instanceObserverKey struct{}

// WithInstanceObserver registers each short-lived instance before it starts.
func WithInstanceObserver(ctx context.Context, observer func(*core.Instance) func()) context.Context {
	return context.WithValue(ctx, instanceObserverKey{}, observer)
}

// FetchThrough — GET url через собранное исходящее out: поднимает свежий
// экземпляр xray с одним этим исходящим, ходит им и закрывает. timeout <= 0
// значит 10 секунд. Это общая часть проверки магазина и агента маяка: оба
// берут готовое исходящее и добывают ответ одним и тем же кодом, чтобы
// проверка с модема не расходилась с проверкой магазина.
//
// Ошибка сборки или запуска экземпляра оборачивает ErrInstance.
func FetchThrough(ctx context.Context, out *core.OutboundHandlerConfig, url string, timeout time.Duration) ([]byte, error) {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// A fresh instance per probe, closed at the end of this call. Reusing
	// one instance across routes would mean one route's outbound
	// configuration leaking into another's dial, which defeats the point of
	// checking each route's own chain in isolation.
	inst, err := NewInstanceWithContext(probeCtx, out)
	if err != nil {
		return nil, err
	}
	if observer, ok := probeCtx.Value(instanceObserverKey{}).(func(*core.Instance) func()); ok {
		defer observer(inst)()
	}
	if err := inst.Start(); err != nil {
		return nil, fmt.Errorf("%w: start probe instance: %w", ErrInstance, err)
	}
	defer inst.Close()

	client := &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			// Клиент одноразовый: без этого соединение после ответа уходит
			// в пул простаивающих, а у своего Transport срока простоя нет —
			// оно, его горутины и канал в закрытый экземпляр живут до конца
			// процесса. На узлах агент так копил сотни МБ за сутки.
			DisableKeepAlives: true,
			// Every dial this client makes — including the TLS handshake
			// http.Transport performs on top for an https URL — goes through
			// xray's dispatcher and out the configured outbound, which is
			// what makes a successful fetch here proof the whole chain
			// works, not just that the entry accepted a connection.
			DialContext: func(dialCtx context.Context, _, addr string) (net.Conn, error) {
				host, portStr, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, fmt.Errorf("split control address %q: %w", addr, err)
				}
				port, err := strconv.Atoi(portStr)
				if err != nil {
					return nil, fmt.Errorf("parse control port %q: %w", portStr, err)
				}
				dest := xnet.TCPDestination(xnet.ParseAddress(host), xnet.Port(port))
				return core.Dial(dialCtx, inst, dest)
			},
		},
	}

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s through the chain: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPStatusError{StatusCode: resp.StatusCode}
	}
	// Bounded read: an address is a few dozen bytes, and both endpoints
	// fetched through here are either ours or a health check. Reading without
	// a limit would let whatever actually answered decide how much memory one
	// probe pass costs — and what answers is, by construction, on the far side
	// of a chain this shop does not control end to end.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return nil, fmt.Errorf("read answer from %s through the chain: %w", url, err)
	}
	return body, nil
}
