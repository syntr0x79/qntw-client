// Package desktopvpn bridges a normal desktop process to its installed UAC
// helper. Pipe closure, Stop and parent death all trigger supervisor cleanup.
package desktopvpn

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"time"

	"github.com/syntr0x79/qntw-client/mobile/windows/elevation"
	"github.com/syntr0x79/qntw-client/mobile/windows/vpn"
)

type deadlineWriter struct {
	conn     io.Writer
	deadline interface{ SetWriteDeadline(time.Time) error }
}

func (w deadlineWriter) Write(b []byte) (int, error) {
	_ = w.deadline.SetWriteDeadline(time.Now().Add(3 * time.Second))
	return w.conn.Write(b)
}

type privateChannel interface {
	io.ReadWriteCloser
	SetWriteDeadline(time.Time) error
	Done() <-chan struct{}
	ExitError() error
}
type channelFactory func(context.Context, string) (privateChannel, error)

func run(ctx context.Context, start elevation.Packet, reader *bufio.Reader, publish func(elevation.Packet), open channelFactory) error {
	if e := elevation.ValidateStart(start, start.Mode); e != nil {
		return e
	}
	if start.Mode == "vpn" {
		if e := vpn.ValidateJob(start.Job); e != nil {
			return e
		}
		if e := vpn.ValidateControlURL(start.ControlURL); e != nil {
			return e
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	controls := make(chan elevation.Packet, 1)
	go func() {
		defer cancel()
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 1024), 4096)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "stop" {
				return
			}
			var v struct {
				Country string `json:"country"`
			}
			if start.Mode != "tinc" || json.Unmarshal([]byte(line), &v) != nil || !elevation.CountryValid(v.Country) {
				return
			}
			select {
			case controls <- elevation.Packet{Type: "country", Country: v.Country}:
			case <-ctx.Done():
				return
			}
		}
	}()
	channel, e := open(ctx, start.Mode)
	if e != nil {
		return e
	}
	defer channel.Close()
	writer := &elevation.Writer{To: deadlineWriter{channel, channel}}
	if e = writer.Write(start); e != nil {
		return e
	}
	events := make(chan elevation.Packet, 1)
	var failedSeen atomic.Bool
	failedPublished := make(chan struct{})
	readDone := make(chan error, 1)
	readFinished := make(chan struct{})
	go func() {
		defer close(readFinished)
		for {
			p, e := elevation.Read(channel)
			if e != nil {
				readDone <- e
				return
			}
			if p.Origin != "" || p.Token != "" || p.Nonce != "" || len(p.Job) > 0 || p.Country != "" || p.Mode != "" || p.ControlURL != "" {
				readDone <- errors.New("invalid helper response")
				return
			}
			if p.Type == "failed" {
				failedSeen.Store(true)
			}
			if p.Type == "accepted" {
				continue
			}
			if p.Type != "status" && p.Type != "failed" && p.Type != "stopped" {
				readDone <- errors.New("invalid helper response")
				return
			}
			select {
			case events <- p:
			default:
				select {
				case <-events:
				default:
				}
				select {
				case events <- p:
				default:
				}
			}
		}
	}()
	// A blocked GUI stdout can never prevent Stop from closing the elevated pipe.
	outputCtx, stopOutput := context.WithCancel(context.Background())
	defer stopOutput()
	go func() {
		for {
			select {
			case <-outputCtx.Done():
				return
			case p := <-events:
				if publish != nil {
					publish(p)
				}
				if p.Type == "failed" {
					select {
					case <-failedPublished:
					default:
						close(failedPublished)
					}
				}
			}
		}
	}()
	flushFailure := func() {
		if failedSeen.Load() {
			select {
			case <-failedPublished:
			case <-time.After(time.Second):
			}
		}
	}
	defer flushFailure()
	waitReader := func() {
		select {
		case <-readFinished:
		case <-time.After(time.Second):
		}
	}
	exitResult := func() error {
		waitReader()
		if failedSeen.Load() {
			return nil
		} // the specific failure is flushed below
		return channel.ExitError()
	}
	shutdown := func() error {
		// Keep the pipe readable until cleanup finishes. Closing it here used
		// to discard the final cleanup failure and turn it into a false success.
		select {
		case <-channel.Done():
			return exitResult()
		case <-time.After(20 * time.Second):
			_ = channel.Close()
			return errors.New("administrator network cleanup is still pending")
		}
	}
	for {
		select {
		case <-ctx.Done():
			if writer.Write(elevation.Packet{Type: "stop"}) != nil {
				_ = channel.Close()
			}
			return shutdown()
		case command := <-controls:
			if e = writer.Write(command); e != nil {
				_ = channel.Close()
				return errors.Join(e, shutdown())
			}
		case e = <-readDone:
			if ctx.Err() != nil {
				return shutdown()
			}
			select {
			case <-channel.Done():
				return exitResult()
			case <-time.After(2 * time.Second):
				_ = channel.Close()
				return errors.Join(e, shutdown())
			}
		case <-channel.Done():
			return exitResult()
		}
	}
}
