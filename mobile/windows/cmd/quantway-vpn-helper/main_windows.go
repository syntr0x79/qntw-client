// The installed administrator supervisor accepts no paths, scripts or command
// strings from its desktop peer. Credentials stay on the authenticated pipe.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/syntr0x79/qntw-client/mobile/clienttinc"
	"github.com/syntr0x79/qntw-client/mobile/windows/elevation"
	"github.com/syntr0x79/qntw-client/mobile/windows/vpn"
	"golang.org/x/sys/windows"
)

func main() {
	idle := flag.Bool("check-idle", false, "check whether package maintenance is safe")
	pipe := flag.String("pipe", "", "private desktop endpoint")
	pid := flag.Uint("caller-pid", 0, "desktop process identity")
	nonce := flag.String("nonce", "", "endpoint nonce")
	broker := flag.Bool("broker", false, "serve the GUI session broker")
	guiPID := flag.Uint("gui-pid", 0, "installed GUI process identity")
	flag.Parse()
	if *idle {
		if elevation.CheckIdle() != nil {
			os.Exit(1)
		}
		return
	}
	// Exclude the working directory and PATH from all DLL resolution. Go/Xray
	// and native tinc load only installed application DLLs or Windows system DLLs.
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("SetDefaultDllDirectories")
	if r, _, _ := proc.Call(0x200 | 0x800); r == 0 {
		os.Exit(1)
	}
	self, e := os.Executable()
	if e != nil {
		os.Exit(1)
	}
	mode := ""
	switch strings.ToLower(filepath.Base(self)) {
	case "quantway-tinc-helper.exe":
		mode = "tinc"
	case "quantway-net-helper.exe":
		mode = "vpn"
	default:
		os.Exit(1)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	if *broker {
		// One UAC approval per GUI session; each VPN session still gets its own
		// fresh elevated helper process started by this broker.
		if mode != "vpn" || *guiPID == 0 {
			os.Exit(1)
		}
		if e = elevation.ServeBroker(ctx, uint32(*guiPID)); e != nil {
			fmt.Fprintln(os.Stderr, "Quantway administrator broker stopped:", e)
			os.Exit(1)
		}
		return
	}
	if e = serve(ctx, mode, *pipe, uint32(*pid), *nonce); e != nil {
		fmt.Fprintln(os.Stderr, "Quantway administrator helper stopped: setup or cleanup failed")
		os.Exit(1)
	}
}

type pipeWriter struct{ net.Conn }

func (w pipeWriter) Write(b []byte) (int, error) {
	_ = w.SetWriteDeadline(time.Now().Add(3 * time.Second))
	return w.Conn.Write(b)
}

func serve(ctx context.Context, mode, endpoint string, parentPID uint32, nonce string) error {
	conn, sid, e := elevation.AcceptParent(ctx, endpoint, parentPID, nonce, mode)
	if e != nil {
		return e
	}
	defer conn.Close()
	writer := &elevation.Writer{To: pipeWriter{conn}}
	fail := func(code string, err error) error {
		_ = writer.Write(elevation.Packet{Type: "failed", Code: code})
		return err
	}
	_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
	request, e := elevation.Read(conn)
	if e != nil {
		return e
	}
	if e = elevation.ValidateStart(request, mode); e != nil {
		return fail("invalid_request", e)
	}
	if mode == "vpn" {
		if e = vpn.ValidateJob(request.Job); e != nil {
			return fail("invalid_profile", e)
		}
		if e = vpn.ValidateControlURL(request.ControlURL); e != nil {
			return fail("invalid_profile", e)
		}
	}
	_ = conn.SetReadDeadline(time.Time{})
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stopRead := context.AfterFunc(ctx, func() { _ = conn.SetReadDeadline(time.Now()) })
	defer stopRead()
	controls := make(chan elevation.Packet, 1)
	go func() {
		defer cancel()
		for {
			p, e := elevation.Read(conn)
			if e != nil || elevation.ValidateControl(p) != nil {
				return
			}
			if p.Type == "stop" {
				return
			}
			if mode != "tinc" {
				return
			}
			select {
			case controls <- p:
			case <-ctx.Done():
				return
			}
		}
	}()
	// A retained pipe handle must not keep a privileged VPN alive after its exact
	// desktop process has exited.
	parent, e := windows.OpenProcess(windows.SYNCHRONIZE, false, parentPID)
	if e != nil {
		return e
	}
	parentWatchDone := make(chan struct{})
	defer func() { cancel(); <-parentWatchDone; windows.CloseHandle(parent) }()
	go func() {
		defer close(parentWatchDone)
		for {
			state, e := windows.WaitForSingleObject(parent, 250)
			if e != nil || state == windows.WAIT_OBJECT_0 {
				cancel()
				return
			}
			if ctx.Err() != nil {
				return
			}
		}
	}()
	release, e := elevation.Acquire()
	if e != nil {
		return fail("vpn_busy", e)
	}
	defer release()
	paths, e := elevation.VerifySelf(mode)
	if e != nil {
		return fail("helper_unsafe", e)
	}
	var state string
	if mode == "tinc" {
		for _, p := range []string{paths.TincDaemon, paths.TincControl, filepath.Join(paths.Bin, "wintun.dll")} {
			if e = elevation.TrustedExecutable(p); e != nil {
				return fail("native_missing", e)
			}
		}
		credentials := clienttinc.Credentials{Origin: request.Origin, Token: request.Token}
		lookup, done := context.WithTimeout(ctx, 12*time.Second)
		catalog, err := clienttinc.NewAPI(credentials, nil).Catalog(lookup)
		done()
		if err != nil {
			return fail("device_authorization_failed", err)
		}
		state, e = elevation.StateDirectory(sid, catalog.DeviceID)
		if e != nil {
			return fail("device_state_unavailable", e)
		}
	} else {
		if e = elevation.TrustedExecutable(filepath.Join(paths.Bin, "wintun.dll")); e != nil {
			return fail("native_missing", e)
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	if e = writer.Write(elevation.Packet{Type: "accepted"}); e != nil {
		return e
	}
	statuses := make(chan clienttinc.Status, 1)
	outputDone := make(chan struct{})
	publish := func(s clienttinc.Status) {
		s = clienttinc.CloneStatus(s)
		select {
		case statuses <- s:
		default:
			select {
			case <-statuses:
			default:
			}
			select {
			case statuses <- s:
			default:
			}
		}
	}
	go func() {
		defer close(outputDone)
		for {
			select {
			case <-ctx.Done():
				return
			case s := <-statuses:
				if writer.Write(elevation.Packet{Type: "status", Status: &s}) != nil {
					cancel()
					return
				}
			}
		}
	}()
	countries := make(chan string, 1)
	finished := make(chan error, 1)
	go func() {
		if mode == "tinc" {
			credentials := clienttinc.Credentials{Origin: request.Origin, Token: request.Token}
			finished <- clienttinc.Run(ctx, clienttinc.Options{Credentials: &credentials, StateDir: state, TincDaemon: paths.TincDaemon, TincControl: paths.TincControl, Country: request.Country, HealthURL: "https://www.gstatic.com/generate_204", OnStatus: publish, Countries: countries})
		} else {
			finished <- vpn.Run(ctx, vpn.Options{Job: request.Job, ControlURL: request.ControlURL, OnStatus: publish})
		}
	}()
	finish := func(err error) error {
		cancel()
		<-outputDone
		if err != nil && !elevation.IsOnlyCancellation(err) {
			code := "network_setup_failed"
			if ctx.Err() != nil {
				code = "cleanup_failed"
			}
			return fail(code, err)
		}
		return writer.Write(elevation.Packet{Type: "stopped"})
	}
	for {
		select {
		case <-ctx.Done():
			return finish(<-finished)
		case e := <-finished:
			return finish(e)
		case p := <-controls:
			select {
			case countries <- p.Country:
			case <-ctx.Done():
				return finish(<-finished)
			}
		}
	}
}
