//go:build darwin && !ios

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/syntr0x79/qntw-client/internal/mobileprobe/wire"
	"github.com/syntr0x79/qntw-client/mobile/clienttinc"
	"github.com/syntr0x79/qntw-client/mobile/engine"
	"github.com/syntr0x79/qntw-client/mobile/linux/probe"
	macnet "github.com/syntr0x79/qntw-client/mobile/macos/network"
	"github.com/syntr0x79/qntw-client/mobile/macos/privilege"
	"golang.org/x/sys/unix"
	"net"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type request struct {
	Mode                  string          `json:"mode"`
	Link                  string          `json:"link"`
	Country               string          `json:"country"`
	Job                   json.RawMessage `json:"job"`
	AcceptanceDiagnostics bool            `json:"acceptance_diagnostics,omitempty"`
}

func main() {
	if run() != nil {
		os.Exit(1)
	}
}
func privateDir(path string) error {
	if e := os.MkdirAll(path, 0700); e != nil {
		return e
	}
	for p := path; p != "/var/db" && p != "/private/var/db" && p != "/"; p = filepath.Dir(p) {
		i, e := os.Lstat(p)
		if e != nil {
			return e
		}
		st, ok := i.Sys().(*syscall.Stat_t)
		if !i.IsDir() || i.Mode()&os.ModeSymlink != 0 || i.Mode().Perm()&0022 != 0 || !ok || st.Uid != 0 {
			return errors.New("unsafe state directory")
		}
	}
	return nil
}
func run() error {
	if os.Geteuid() != 0 || len(os.Args) != 2 {
		return errors.New("administrator required")
	}
	originalUID := os.Getuid()
	if e := unix.Setgid(0); e != nil {
		return e
	}
	if e := unix.Setuid(0); e != nil {
		return e
	}
	unix.Umask(0077)
	conn, e := net.DialUnix("unix", nil, &net.UnixAddr{Name: os.Args[1], Net: "unix"})
	if e != nil {
		return e
	}
	defer conn.Close()
	uid, e := privilege.PeerUID(conn)
	if e != nil || uid == 0 {
		return errors.New("invalid app peer")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	reader := bufio.NewReader(conn)
	conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	line, e := reader.ReadString('\n')
	if e != nil || len(line) > 1<<20 {
		return errors.New("invalid request")
	}
	conn.SetReadDeadline(time.Time{})
	var req request
	if json.Unmarshal([]byte(line), &req) != nil {
		return errors.New("invalid request")
	}
	var mu sync.Mutex
	emit := func(v any) {
		mu.Lock()
		defer mu.Unlock()
		conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		if json.NewEncoder(conn).Encode(v) != nil {
			cancel()
		}
	}
	emit(map[string]any{"type": "diagnostic", "stage": "identity", "helper_pid": os.Getpid(), "original_uid": originalUID, "real_uid": os.Getuid(), "effective_uid": os.Geteuid()})
	fd, e := unix.Open("/var/run/quantway-vpn.lock", unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0600)
	if e != nil {
		return e
	}
	defer unix.Close(fd)
	if unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		emit(map[string]string{"status": "failed", "error": "Другое подключение Quantway уже работает."})
		return errors.New("busy")
	}
	defer unix.Flock(fd, unix.LOCK_UN)
	// Read stop/EOF immediately, including while admission is still waiting on
	// the API. The country endpoint becomes available only after validating its
	// fixed private state directory; the app cannot provide or replace a path.
	var controlDirectory atomic.Pointer[string]
	go consumeCommands(ctx, reader, func() string {
		if dir := controlDirectory.Load(); dir != nil {
			return *dir
		}
		return ""
	}, req.AcceptanceDiagnostics, cancel, emit)
	if req.Mode == "tinc" {
		u, err := url.Parse(req.Link)
		token := regexp.MustCompile(`^/sub/([A-Za-z0-9_-]{16,256})/?$`)
		if err != nil || u.Scheme != "https" || u.Host != "qntw.link" || u.User != nil || u.Fragment != "" || !token.MatchString(u.EscapedPath()) || !regexp.MustCompile(`^[A-Z]{2}$`).MatchString(req.Country) {
			return errors.New("invalid device subscription")
		}
		creds := clienttinc.Credentials{Origin: "https://qntw.link", Token: token.FindStringSubmatch(u.Path)[1]}
		catalog, err := clienttinc.NewAPI(creds, nil).Catalog(ctx)
		if err != nil {
			emit(map[string]string{"status": "failed", "error": "Не удалось получить доступ к Tinc. Проверьте подписку."})
			return err
		}
		dir := fmt.Sprintf("/var/db/quantway/users/%d/%s", uid, catalog.DeviceID.String())
		if e = privateDir(dir); e != nil {
			return e
		}
		controlDirectory.Store(&dir)
		exe, _ := os.Executable()
		bin := filepath.Dir(exe)
		var startupTimedOut atomic.Bool
		timer := time.AfterFunc(45*time.Second, func() { startupTimedOut.Store(true); cancel() })
		defer timer.Stop()
		e = clienttinc.Run(ctx, clienttinc.Options{Credentials: &creds, StateDir: dir, TincDaemon: filepath.Join(bin, "tincd"), TincControl: filepath.Join(bin, "tinc"), Country: req.Country, DNSMode: "scutil", HealthURL: "https://qntw.link/healthz", OnStatus: func(s clienttinc.Status) {
			if s.State == "connected" {
				timer.Stop()
			}
			emit(struct {
				clienttinc.Status
				StatusName string                            `json:"status"`
				Transport  clienttinc.AssignedTransportStats `json:"transport"`
			}{s, s.State, clienttinc.CurrentAssignedTransportStats()})
		}})
		if startupTimedOut.Load() && !errors.Is(e, macnet.ErrCleanupFailed) {
			emit(map[string]string{"status": "failed", "code": "startup_timeout", "error": "Tinc не подтвердил передачу данных за 45 секунд. Попытка подключения остановлена."})
			return errors.New("startup timeout")
		}
	} else if req.Mode == "vpn" {
		var j wire.Job
		if json.Unmarshal(req.Job, &j) != nil || (j.Engine != "xray" && j.Engine != "amneziawg") {
			return errors.New("invalid route")
		}
		address, dns, err := tunnelSettings(j)
		if err != nil {
			return err
		}
		physical, err := probe.VPNNetwork()
		if err != nil {
			return err
		}
		s, err := macnet.Open(ctx, true)
		if err != nil {
			emit(map[string]string{"status": "failed", "error": "Не удалось настроить системный туннель."})
			return err
		}
		emit(map[string]string{"status": "connecting", "message": "Настраиваем туннель и проверяем HTTPS и DNS."})
		e = runOrdinaryVPN(ctx, j, physical, s, address.Addr().String(), dns.String(), func(startup context.Context, jobJSON string) error {
			if err := macnet.Configure(startup, s.System, address.Addr().String()); err != nil {
				return err
			}
			return engine.StartVPN(int(s.System.File.Fd()), jobJSON, physical)
		}, engine.StopVPN, func() { emit(map[string]string{"status": "connected"}) })
	} else {
		return errors.New("invalid mode")
	}
	if e != nil && e != context.Canceled {
		var connectionError macnet.ConnectionError
		if errors.Is(e, macnet.ErrCleanupFailed) {
			emit(map[string]string{"status": "failed", "code": "network_cleanup_failed", "error": "Сетевые настройки восстановлены не полностью. Интернет может не работать. Не запускайте новое подключение до восстановления сети."})
		} else if errors.As(e, &connectionError) {
			emit(map[string]any{"status": "failed", "code": connectionError.Code, "error": connectionError.Message, "diagnostics": connectionError.Diagnostics})
		} else {
			emit(map[string]string{"status": "failed", "error": "Подключение завершилось с ошибкой. Проверьте сеть перед повторным запуском."})
		}
		return e
	}
	emit(map[string]string{"status": "stopped"})
	return nil
}

func tunnelSettings(job wire.Job) (netip.Prefix, netip.Addr, error) {
	address := netip.MustParsePrefix("172.19.0.1/32")
	dns := netip.MustParseAddr("1.1.1.1")
	if job.Engine == "xray" {
		return address, dns, nil
	}
	if job.Engine != "amneziawg" {
		return netip.Prefix{}, netip.Addr{}, errors.New("unsupported engine")
	}
	found := false
	section := ""
	for _, line := range strings.Split(job.AWGConf, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "[") {
			section = strings.ToLower(line)
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || section != "[interface]" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "address":
			for _, item := range strings.Split(value, ",") {
				if prefix, err := netip.ParsePrefix(strings.TrimSpace(item)); err == nil && prefix.Addr().Is4() {
					address = netip.PrefixFrom(prefix.Addr(), 32)
					found = true
					break
				}
			}
		case "dns":
			for _, item := range strings.Split(value, ",") {
				if ip, err := netip.ParseAddr(strings.TrimSpace(item)); err == nil && ip.Is4() && !ip.IsLoopback() && !ip.IsUnspecified() && !ip.IsMulticast() {
					dns = ip
					break
				}
			}
		}
	}
	if !found {
		return netip.Prefix{}, netip.Addr{}, errors.New("missing tunnel address")
	}
	return address, dns, nil
}
