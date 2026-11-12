//go:build darwin && !ios

package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/syntr0x79/qntw-client/internal/mobileprobe/wire"
	"github.com/syntr0x79/qntw-client/mobile/engine"
	macnet "github.com/syntr0x79/qntw-client/mobile/macos/network"
)

type ordinarySession interface {
	AllowPhysicalIPs(context.Context, []string) error
	PinControlPlane(context.Context) error
	ApplyDNS(context.Context, []string) error
	Activate(context.Context) error
	VerifySystemFrom(context.Context, string) error
	Close() error
}

// A successful userspace preflight does not prove that macOS can use the TUN.
// Keep startup bounded and announce connection only after the real system path
// passes HTTPS, DNS and resolver-policy checks. Every exit owns its cleanup.
func runOrdinaryVPN(ctx context.Context, job wire.Job, physical engine.Network, session ordinarySession, address, dns string, start func(context.Context, string) error, stop, connected func()) (err error) {
	defer func() {
		stop()
		if ctx.Err() != nil && !errors.Is(err, macnet.ErrCleanupFailed) {
			err = nil
		}
		err = errors.Join(err, session.Close())
	}()
	startup, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	allow := func(ctx context.Context, addresses []string) error {
		if err := session.AllowPhysicalIPs(ctx, addresses); err != nil {
			return macnet.ConnectionError{Code: "physical_route_failed", Message: "Не удалось закрепить VPN-сервер за обычной сетью. Подключение остановлено."}
		}
		return nil
	}
	job, err = prepareOrdinaryRoute(startup, job, physical, allow)
	if err != nil {
		return err
	}
	if err = session.PinControlPlane(startup); err != nil {
		return macnet.ConnectionError{Code: "physical_route_failed", Message: "Не удалось подготовить проверку системного туннеля. Подключение остановлено."}
	}
	payload, err := json.Marshal(job)
	if err != nil {
		return errors.New("invalid route")
	}
	if err = startup.Err(); err != nil {
		return err
	}
	if err = start(startup, string(payload)); err != nil {
		return err
	}
	if err = session.ApplyDNS(startup, []string{dns}); err != nil {
		return err
	}
	if err = session.Activate(startup); err != nil {
		return err
	}
	if err = session.VerifySystemFrom(startup, address); err != nil {
		return err
	}
	if err = startup.Err(); err != nil {
		return err
	}
	cancel()
	connected()
	<-ctx.Done()
	return nil
}
