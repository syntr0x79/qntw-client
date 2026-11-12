//go:build linux && !android

package tincvpn

import (
	"context"
	"errors"
	"net"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/syntr0x79/qntw-client/mobile/clienttinc"
	"github.com/syntr0x79/qntw-client/mobile/linux/netlock"
)

type serverOptions struct {
	stateRoot string
	verify    func() error
	lock      func() (*os.File, error)
	device    func(context.Context, clienttinc.Credentials) (uuid.UUID, error)
	run       func(context.Context, clienttinc.Options) error
	country   func(context.Context, string, string) error
}

func productionServer() serverOptions {
	return serverOptions{
		stateRoot: StateRoot,
		verify: func() error {
			if os.Geteuid() != 0 {
				return errors.New("administrator privileges are required")
			}
			for _, path := range []string{HelperPath, NativeDaemon, NativeControl} {
				if err := trustedExecutable(path); err != nil {
					if path != HelperPath && FailureCode(err) == "helper_missing" {
						return failure("native_missing")
					}
					return err
				}
			}
			return nil
		},
		lock: netlock.Acquire,
		device: func(ctx context.Context, creds clienttinc.Credentials) (uuid.UUID, error) {
			catalog, err := clienttinc.NewAPI(creds, nil).Catalog(ctx)
			if err != nil {
				return uuid.Nil, err
			}
			if catalog.DeviceID == uuid.Nil {
				return uuid.Nil, failure("device_authorization_failed")
			}
			return catalog.DeviceID, nil
		},
		run: clienttinc.Run,
		country: func(ctx context.Context, state, code string) error {
			_, err := clienttinc.Control(ctx, state, "country", code)
			return err
		},
	}
}

// Serve runs a single dedicated tinc session on an inherited private socket.
// The caller identity comes from pkexec and must match socket SO_PEERCRED. No
// path or executable is accepted from the unprivileged caller.
func Serve(ctx context.Context, conn *net.UnixConn, uid uint32) error {
	return serve(ctx, conn, uid, productionServer())
}

func serve(ctx context.Context, conn *net.UnixConn, uid uint32, o serverOptions) (result error) {
	if conn == nil {
		return errors.New("missing private channel")
	}
	defer conn.Close()
	if err := checkPeer(conn, uid); err != nil {
		return err
	}
	w := &writer{conn: conn}
	accepted := false
	defer func() {
		if result != nil && !accepted {
			code := FailureCode(result)
			if code == "network_setup_failed" {
				code = "helper_initialization_failed"
			}
			_ = w.send(packet{Type: "error", Code: code, Error: "Tinc helper initialization failed"})
		}
	}()
	if err := o.verify(); err != nil {
		return err
	}
	_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
	p, err := receive(conn)
	if err != nil || p.Type != "start" || p.ID != 0 || p.Status != nil || p.Error != "" || p.Code != "" {
		return errors.New("invalid start request")
	}
	creds, err := desktopCredentials(p.Origin, p.Token, p.Country)
	if err != nil {
		return err
	}
	country := p.Country
	wantMesh := p.WantMesh
	p = packet{} // Do not retain a second credential-bearing protocol packet.
	_ = conn.SetReadDeadline(time.Time{})
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// Parent EOF also cancels authentication, which precedes every state write
	// and network action. The reader accepts bounded commands only.
	requests := make(chan packet, 1)
	go func() {
		defer cancel()
		for {
			r, err := receive(conn)
			if err != nil {
				return
			}
			if r.Origin != "" || r.Token != "" || r.Status != nil || r.Error != "" || r.Code != "" || r.WantMesh || r.ID == 0 || (r.Type != "country" && r.Type != "stop") || (r.Type == "country" && !countryValid(r.Country)) || (r.Type == "stop" && r.Country != "") {
				return
			}
			select {
			case requests <- r:
			case <-ctx.Done():
				return
			}
		}
	}()
	stopClose := context.AfterFunc(ctx, func() { _ = conn.SetReadDeadline(time.Now()) })
	defer stopClose()
	lock, err := o.lock()
	if err != nil {
		return failure("vpn_lock_failed")
	}
	defer lock.Close() // Released only after runtime cleanup has returned.
	lookupCtx, lookupCancel := context.WithTimeout(ctx, 12*time.Second)
	device, err := o.device(lookupCtx, creds)
	lookupCancel()
	if err != nil || device == uuid.Nil {
		code := FailureCode(err)
		if code == "network_setup_failed" {
			code = "device_authorization_failed"
		}
		return failure(code)
	}
	if ctx.Err() != nil {
		return nil
	}
	state, err := stateDirectory(o.stateRoot, uid, device)
	if err != nil {
		return failure("device_state_unavailable")
	}
	if err = w.send(packet{Type: "accepted"}); err != nil {
		return err
	}
	accepted = true
	statuses := make(chan Status, 1)
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		for {
			select {
			case s := <-statuses:
				if !wantMesh {
					s.Mesh = nil
				}
				if w.send(packet{Type: "status", Status: &s}) != nil {
					cancel()
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	finished := make(chan error, 1)
	go func() {
		runErr := o.run(ctx, clienttinc.Options{
			Credentials: &creds, StateDir: state, TincDaemon: NativeDaemon, TincControl: NativeControl,
			Country: country, DNSMode: "resolved", HealthURL: "https://www.gstatic.com/generate_204",
			OnStatus: func(s Status) { keepLatest(statuses, s) },
		})
		if ctx.Err() != nil && (errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded)) {
			runErr = nil
		}
		finished <- runErr
	}()
	finish := func(runErr error, stopID uint64) error {
		cancel()
		<-writeDone
		if runErr != nil {
			code := FailureCode(runErr)
			_ = w.send(packet{Type: "error", Code: code, Error: "Tinc network setup failed"})
			return failure(code)
		}
		if stopID != 0 {
			return w.send(packet{Type: "stopped", ID: stopID})
		}
		return nil
	}
	var stopID uint64
	for {
		select {
		case <-ctx.Done():
			// Wait for interfaces, firewall, DNS and children cleanup.
			return finish(<-finished, stopID)
		case err = <-finished:
			return finish(err, stopID)
		case req := <-requests:
			if req.Type == "stop" {
				stopID = req.ID
				cancel()
				continue
			}
			countryCtx, countryCancel := context.WithTimeout(ctx, 5*time.Second)
			err := o.country(countryCtx, state, req.Country)
			countryCancel()
			reply := packet{Type: "country", ID: req.ID}
			if err != nil {
				reply.Error = "Country change is not ready"
			}
			if w.send(reply) != nil {
				cancel()
			}
		}
	}
}
