//go:build darwin && !ios

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"github.com/syntr0x79/qntw-client/mobile/macos/privilege"
	"os"
	"os/signal"
	"syscall"
)

func runMac(mode string, req request, reader *bufio.Reader) {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	reportedFailure := false
	e := privilege.Run(ctx, map[string]any{"mode": mode, "link": req.Link, "country": req.Country, "job": req.Job, "acceptance_diagnostics": req.AcceptanceDiagnostics}, reader, func(v json.RawMessage) {
		var event struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		}
		if json.Unmarshal(v, &event) == nil && (event.Status == "failed" || event.Error != "") {
			reportedFailure = true
		}
		_, _ = responseOut.Write(append(v, '\n'))
	})
	if e != nil && !reportedFailure {
		_ = json.NewEncoder(responseOut).Encode(map[string]string{"status": "failed", "error": e.Error()})
	}
}
func connect(req request, reader *bufio.Reader)     { runMac("vpn", req, reader) }
func tincConnect(req request, reader *bufio.Reader) { runMac("tinc", req, reader) }
