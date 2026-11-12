//go:build linux

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/syntr0x79/qntw-client/mobile/linux/tincvpn"
)

func publicTincFailure(err error) map[string]string {
	code := tincvpn.FailureCode(err)
	return map[string]string{"status": "failed", "code": code, "error": tincvpn.FailureMessage(code)}
}

func tincConnect(req request, reader *bufio.Reader) {
	if os.Geteuid() == 0 {
		_ = json.NewEncoder(responseOut).Encode(publicTincFailure(tincvpn.Failure{Code: "run_as_user"}))
		return
	}
	credentials, err := tincCredentials(req.Link)
	if err != nil || !countryCode.MatchString(req.Country) {
		_ = json.NewEncoder(responseOut).Encode(map[string]string{"status": "failed", "error": "Выберите подписку Quantway и страну подключения."})
		return
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer cancel()
	countries := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 1024), 4096)
		defer cancel()
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "stop" {
				return
			}
			var command struct {
				Country string `json:"country"`
			}
			if json.Unmarshal([]byte(line), &command) != nil || !countryCode.MatchString(command.Country) {
				return
			}
			select {
			case countries <- command.Country:
			case <-ctx.Done():
				return
			}
		}
	}()
	updates := make(chan tincvpn.Status, 1)
	var outputMu sync.Mutex
	write := func(event any) error {
		outputMu.Lock()
		defer outputMu.Unlock()
		return json.NewEncoder(responseOut).Encode(event)
	}
	publish := func(status tincvpn.Status) {
		state, include := publicTincState(status.State)
		if !include {
			return
		}
		status.State = state
		select {
		case updates <- status:
		default:
			select {
			case <-updates:
			default:
			}
			select {
			case updates <- status:
			default:
			}
		}
	}
	// Output backpressure must never keep an elevated network session alive
	// after GTK closes its input, cancels, or exits.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case status := <-updates:
				event := struct {
					tincvpn.Status
					StatusName string `json:"status"`
				}{Status: status, StatusName: status.State}
				if write(event) != nil {
					cancel()
					return
				}
			}
		}
	}()
	session, err := tincvpn.Start(ctx, tincvpn.Config{Origin: credentials.Origin, Token: credentials.Token, Country: req.Country, OnStatus: publish})
	if err != nil {
		if ctx.Err() == nil {
			_ = write(publicTincFailure(err))
		}
		return
	}
	defer func() {
		if err := session.Close(); err != nil {
			// Cleanup already completed or failed closed; a blocked GUI output
			// must not keep this wrapper alive or hide the stop failure.
			written := make(chan struct{})
			go func() {
				_ = write(publicTincFailure(err))
				close(written)
			}()
			select {
			case <-written:
			case <-time.After(time.Second):
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-session.Done():
			return
		case country := <-countries:
			requestCtx, done := context.WithTimeout(ctx, 8*time.Second)
			err := session.Country(requestCtx, country)
			done()
			if err != nil && ctx.Err() == nil {
				status := session.Status()
				status.Message = "Не удалось изменить страну. Сохранено прежнее подключение."
				publish(status)
			}
		}
	}
}
