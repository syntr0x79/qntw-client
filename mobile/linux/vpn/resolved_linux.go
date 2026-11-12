//go:build linux

package vpn

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

var interfaceName = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,15}$`)

// Resolved changes only settings for the temporary TUN link. It requires the
// machine to use systemd-resolved's local stub; other DNS managers fail closed.
type Resolved struct {
	resolvConf string
	run        func(context.Context, ...string) error
}

func (r Resolved) command(ctx context.Context, args ...string) error {
	if r.run != nil {
		return r.run(ctx, args...)
	}
	limited, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return exec.CommandContext(limited, "/usr/bin/resolvectl", args...).Run()
}
func (r Resolved) Apply(ctx context.Context, name string, ip netip.Addr) error {
	if !interfaceName.MatchString(name) || !ip.IsValid() {
		return errors.New("invalid DNS configuration")
	}
	path := r.resolvConf
	if path == "" {
		path = "/etc/resolv.conf"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return errors.New("cannot inspect system DNS")
	}
	found := false
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "nameserver" {
			if fields[1] != "127.0.0.53" && fields[1] != "127.0.0.54" {
				return errors.New("systemd-resolved DNS stub is required")
			}
			found = true
		}
	}
	if !found {
		return errors.New("systemd-resolved DNS stub is required")
	}
	if err = r.command(ctx, "dns", name, ip.String()); err != nil {
		return fmt.Errorf("set link DNS: %w", err)
	}
	if err = r.command(ctx, "domain", name, "~."); err != nil {
		return fmt.Errorf("set link DNS domain: %w", err)
	}
	if err = r.command(ctx, "default-route", name, "yes"); err != nil {
		return fmt.Errorf("set link DNS route: %w", err)
	}
	return nil
}
func (r Resolved) Revert(ctx context.Context, name string) error {
	if !interfaceName.MatchString(name) {
		return errors.New("invalid interface")
	}
	return r.command(ctx, "revert", name)
}
