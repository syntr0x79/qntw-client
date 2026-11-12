// Package awgprobe — зонд AWG-входа: поднимает клиентский туннель
// amneziawg-go в userspace (netstack, без TUN и root) и проверяет, что
// через него открывается ControlURL. Работает в магазине, в агенте узла и
// на коробке с модемами.
package awgprobe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/amnezia-vpn/amneziawg-go/v3/conn"
	"github.com/amnezia-vpn/amneziawg-go/v3/device"
	"github.com/amnezia-vpn/amneziawg-go/v3/tun/netstack"
)

type Options struct {
	// Listen открывает UDP-сокет туннеля; nil — обычный. У коробки — с SO_BINDTODEVICE.
	Listen func(network string, laddr *net.UDPAddr) (*net.UDPConn, error)
	// Resolve — имя сервера в адрес; nil — системный резолвер.
	Resolve    func(ctx context.Context, host string) (netip.Addr, error)
	ControlURL string
	Timeout    time.Duration // 0 → 15 * time.Second
}

var ErrHandshake = errors.New("awgprobe: нет рукопожатия")

// HTTPStatusError identifies a rejected control response without retaining its URL.
type HTTPStatusError struct {
	StatusCode int
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("awgprobe: control request returned HTTP %d", e.StatusCode)
}

func (e *HTTPStatusError) HTTPStatusCode() int { return e.StatusCode }

// ValidateConfig checks the client configuration without opening any sockets.
func ValidateConfig(conf string) error {
	_, err := parseConf(conf)
	return err
}

const defaultTimeout = 15 * time.Second

// Probe поднимает userspace-туннель по .conf (формат topology.BuildAWGConf),
// тянет ControlURL через него (2xx — nil), всё закрывает.
//
// Timeout — на всю проверку: резолв, рукопожатие и запрос. Рукопожатие
// ждём отдельно, чтобы отличить «вход молчит» (ErrHandshake) от «туннель
// есть, а дальше не пускают». Имя из ControlURL резолвится внутри туннеля
// DNS-сервером из .conf (так делает netstack.Net.DialContext) — как у
// настоящего клиента; адрес-литерал DNS не трогает.
func Probe(ctx context.Context, conf string, o Options) error {
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	c, err := parseConf(conf)
	if err != nil {
		return err
	}
	uapi, ep, err := c.uapi(ctx, o.Resolve)
	if err != nil {
		return err
	}
	network := "udp4"
	if ep.Addr().Is6() {
		network = "udp6"
	}
	tn, tnet, err := netstack.CreateNetTUN([]netip.Addr{c.address}, c.dns, c.mtu)
	if err != nil {
		return fmt.Errorf("awgprobe: netstack: %w", err)
	}
	dev := device.NewDevice(tn, newBind(o.Listen, network), device.NewLogger(device.LogLevelSilent, ""))
	defer dev.Close() // закрывает и netstack, и сокет
	if err := dev.IpcSet(uapi); err != nil {
		return fmt.Errorf("awgprobe: настройка устройства: %w", err)
	}
	flushStaleRead(tnet)
	if err := dev.Up(); err != nil {
		return fmt.Errorf("awgprobe: %w", err)
	}
	if err := waitHandshake(ctx, dev); err != nil {
		return err
	}

	client := &http.Client{Transport: &http.Transport{DialContext: tnet.DialContext, DisableKeepAlives: true}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, o.ControlURL, nil)
	if err != nil {
		return fmt.Errorf("awgprobe: ControlURL: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("awgprobe: запрос через туннель: %w", err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &HTTPStatusError{StatusCode: resp.StatusCode}
	}
	return nil
}

// flushStaleRead отдаёт устройству пакет-жертву между IpcSet и Up. TUN
// reader amneziawg-go (v3.1.20260828, device/send.go RoutineReadFromTUN)
// берёт S4 в начале итерации и встаёт в Read ещё в NewDevice, до IpcSet, —
// первый прочитанный пакет уходит без транспортного паддинга, и узел
// выбрасывает его как «unknown type». Без жертвы это SYN запроса к
// ControlURL: секунда на повтор SYN (замерено). Пока устройство не поднято,
// пир не запущен и жертва никуда не уходит, а следующая итерация читает
// уже с верным S4.
func flushStaleRead(tnet *netstack.Net) {
	c, err := tnet.DialUDPAddrPort(netip.AddrPort{}, netip.MustParseAddrPort("192.0.2.1:9"))
	if err != nil {
		return
	}
	_, _ = c.Write([]byte{0})
	c.Close()
}

// waitHandshake ждёт, пока устройство сообщит время последнего
// рукопожатия. Первое рукопожатие запускает keepalive (uapi.go).
func waitHandshake(ctx context.Context, dev *device.Device) error {
	t := time.NewTicker(50 * time.Millisecond)
	defer t.Stop()
	for {
		if s, err := dev.IpcGet(); err == nil && handshaken(s) {
			return nil
		}
		select {
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return ErrHandshake
			}
			return ctx.Err()
		case <-t.C:
		}
	}
}

func handshaken(ipcGet string) bool {
	for _, l := range strings.Split(ipcGet, "\n") {
		if v, ok := strings.CutPrefix(l, "last_handshake_time_sec="); ok && v != "0" {
			return true
		}
	}
	return false
}

// bind — conn.Bind на одном UDP-сокете из Options.Listen. Свой, а не
// conn.StdNetBind: у того сокет открывает внутренняя listenNet, подменить
// её нельзя, а коробке нужен сокет, привязанный к модему (SO_BINDTODEVICE).
// Пакеты по одному (BatchSize 1) — зонду пропускная способность не нужна.
type bind struct {
	listen  func(network string, laddr *net.UDPAddr) (*net.UDPConn, error)
	network string

	mu sync.Mutex
	c  *net.UDPConn
}

func newBind(listen func(string, *net.UDPAddr) (*net.UDPConn, error), network string) *bind {
	if listen == nil {
		listen = net.ListenUDP
	}
	return &bind{listen: listen, network: network}
}

func (b *bind) Open(port uint16) ([]conn.ReceiveFunc, uint16, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.c != nil {
		return nil, 0, conn.ErrBindAlreadyOpen
	}
	c, err := b.listen(b.network, &net.UDPAddr{Port: int(port)})
	if err != nil {
		return nil, 0, err
	}
	b.c = c
	recv := func(packets [][]byte, sizes []int, eps []conn.Endpoint) (int, error) {
		n, addr, err := c.ReadFromUDPAddrPort(packets[0])
		if err != nil {
			return 0, err
		}
		sizes[0], eps[0] = n, endpoint(netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port()))
		return 1, nil
	}
	return []conn.ReceiveFunc{recv}, uint16(c.LocalAddr().(*net.UDPAddr).Port), nil
}

func (b *bind) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.c == nil {
		return nil
	}
	err := b.c.Close()
	b.c = nil
	return err
}

func (b *bind) SetMark(uint32) error { return nil }

func (b *bind) Send(bufs [][]byte, ep conn.Endpoint) error {
	b.mu.Lock()
	c := b.c
	b.mu.Unlock()
	if c == nil {
		return net.ErrClosed
	}
	e, ok := ep.(endpoint)
	if !ok {
		return conn.ErrWrongEndpointType
	}
	for _, buf := range bufs {
		if _, err := c.WriteToUDPAddrPort(buf, netip.AddrPort(e)); err != nil {
			return err
		}
	}
	return nil
}

func (b *bind) ParseEndpoint(s string) (conn.Endpoint, error) {
	ap, err := netip.ParseAddrPort(s)
	if err != nil {
		return nil, err
	}
	return endpoint(netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port())), nil
}

func (b *bind) BatchSize() int { return 1 }

type endpoint netip.AddrPort

func (e endpoint) ClearSrc()           {}
func (e endpoint) SrcToString() string { return "" }
func (e endpoint) DstToString() string { return netip.AddrPort(e).String() }
func (e endpoint) DstToBytes() []byte {
	b, _ := netip.AddrPort(e).MarshalBinary()
	return b
}
func (e endpoint) DstIP() netip.Addr { return netip.AddrPort(e).Addr() }
func (e endpoint) SrcIP() netip.Addr { return netip.Addr{} }
