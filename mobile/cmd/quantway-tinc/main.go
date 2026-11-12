//go:build linux && !android

// quantway-tinc is a foreground, root-only Linux pilot. It is independent of
// the current desktop UI and stores credentials only in a private config file.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/syntr0x79/qntw-client/mobile/clienttinc"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: quantway-tinc connect|status|country|stop|check [options]")
	}
	action := os.Args[1]
	flags := flag.NewFlagSet(action, flag.ContinueOnError)
	state := flags.String("state", "/var/lib/quantway-tinc", "private pilot state directory")
	config := flags.String("config", "/etc/quantway-tinc/device.json", "private credentials JSON (0600); token is never a command argument")
	country := flags.String("country", "", "two-letter exit country")
	iface := flags.String("interface", "", "physical interface; defaults to the IPv4 default route")
	socks := flags.Bool("socks-only", false, "overlay + loopback SOCKS1089 without system routes/DNS")
	dns := flags.String("dns", "resolved", "system DNS mode: resolved or none (isolated test only)")
	health := flags.String("health-url", "https://www.gstatic.com/generate_204", "HTTPS forwarding check through the authorized exit")
	executable, e := os.Executable()
	if e != nil {
		return e
	}
	dir := filepath.Dir(executable)
	daemon := flags.String("tincd", filepath.Join(dir, "tincd"), "guarded client native daemon")
	control := flags.String("tinc", filepath.Join(dir, "tinc"), "matching native control utility")
	if e = flags.Parse(os.Args[2:]); e != nil {
		return e
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	switch action {
	case "connect":
		return clienttinc.Run(ctx, clienttinc.Options{CredentialsPath: *config, StateDir: *state, TincDaemon: *daemon, TincControl: *control, Country: *country, Interface: *iface, SOCKSOnly: *socks, DNSMode: *dns, HealthURL: *health})
	case "status", "country", "stop":
		b, e := clienttinc.Control(ctx, *state, action, *country)
		if e != nil {
			return e
		}
		_, e = os.Stdout.Write(b)
		return e
	case "check":
		if e = clienttinc.CheckSOCKS(ctx, *health); e != nil {
			return e
		}
		fmt.Println("forwarding check passed")
		return nil
	default:
		return fmt.Errorf("unknown command")
	}
}
