//go:build darwin && !ios

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"runtime"
	"time"

	"github.com/syntr0x79/qntw-client/mobile/clienttinc"
	"github.com/syntr0x79/qntw-client/mobile/macos/privilege"
)

// The authenticated app can only control this already-owned session. It cannot
// select filesystem paths, replace credentials, or launch privileged commands.
func consumeCommands(ctx context.Context, reader io.Reader, stateDirectory func() string, diagnostics bool, cancel context.CancelFunc, emit func(any)) {
	defer cancel()
	_ = privilege.ReadSessionCommands(reader, func(command privilege.SessionCommand) error {
		switch command.Action {
		case "stop":
			return nil
		case "country":
			stateDir := stateDirectory()
			if stateDir == "" {
				return errors.New("country requires an active tinc session")
			}
			controlCtx, done := context.WithTimeout(ctx, 6*time.Second)
			_, err := clienttinc.Control(controlCtx, stateDir, "country", command.Country)
			done()
			if err != nil {
				emit(map[string]string{"type": "diagnostic", "stage": "control_failed", "code": "country_command_failed"})
				return err
			}
			emit(map[string]any{"type": "diagnostic", "stage": "country_requested", "country": command.Country, "helper_pid": os.Getpid()})
		case "collect":
			if !diagnostics || stateDirectory() == "" {
				return errors.New("acceptance diagnostics are disabled")
			}
			// Trigger finalizers from retired assignments before verifying the new
			// path. This reproduces the borrowed-FD lifetime defect on old Xray.
			runtime.GC()
			runtime.Gosched()
			runtime.GC()
			emit(map[string]any{"type": "diagnostic", "stage": "garbage_collected", "helper_pid": os.Getpid()})
		}
		return nil
	})
}
