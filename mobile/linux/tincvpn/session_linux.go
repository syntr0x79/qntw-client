//go:build linux && !android

package tincvpn

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"
)

type Config struct {
	Origin  string
	Token   string
	Country string
	// OnStatus receives public connection state. Slow callbacks are coalesced
	// and never block the helper's fail-closed lease enforcement.
	OnStatus func(Status)
	// Private hooks are available only to package lifecycle tests.
	launch func(*os.File) (<-chan error, error)
}

type Session struct {
	conn      *net.UnixConn
	writer    writer
	done      chan struct{}
	readDone  chan struct{}
	pending   map[uint64]chan packet
	callbacks chan Status
	mu        sync.RWMutex
	status    Status
	err       error
	requestMu sync.Mutex
	nextID    uint64
	closeOnce sync.Once
	closed    chan struct{}
	closeErr  error
}

var lifecycle struct {
	sync.Mutex
	active bool
}

func launchHelper(child *os.File) (<-chan error, error) {
	for _, path := range []string{HelperPath, "/usr/bin/pkexec"} {
		if err := trustedExecutable(path); err != nil {
			if path == "/usr/bin/pkexec" {
				if FailureCode(err) == "helper_missing" {
					return nil, failure("pkexec_missing")
				}
				return nil, failure("pkexec_unsafe")
			}
			return nil, err
		}
	}
	// Closing stdin is the cleanup signal. Killing a root helper could strand
	// its firewall or DNS state, so cleanup does not rely on Process.Kill.
	cmd := exec.Command("/usr/bin/pkexec", HelperPath)
	cmd.Stdin = child
	cmd.Dir = "/"
	if err := cmd.Start(); err != nil {
		return nil, failure("helper_start_failed")
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); close(done) }()
	return done, nil
}

// Start authorizes the dedicated installed helper. Credentials travel over a
// private inherited socket, never command arguments, logs or a temporary file.
// The session starts in preparing; OnStatus reports actual path readiness.
func Start(ctx context.Context, c Config) (*Session, error) {
	if _, err := desktopCredentials(c.Origin, c.Token, c.Country); err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	lifecycle.Lock()
	if lifecycle.active {
		lifecycle.Unlock()
		return nil, failure("vpn_busy")
	}
	lifecycle.active = true
	lifecycle.Unlock()
	success := false
	defer func() {
		if !success {
			lifecycle.Lock()
			lifecycle.active = false
			lifecycle.Unlock()
		}
	}()
	parent, child, err := newPair()
	if err != nil {
		return nil, failure("helper_protocol_failed")
	}
	launch := c.launch
	if launch == nil {
		launch = launchHelper
	}
	processDone, err := launch(child)
	_ = child.Close()
	if err != nil {
		parent.Close()
		return nil, err
	}
	stopClose := context.AfterFunc(ctx, func() { parent.Close() })
	defer stopClose()
	w := writer{conn: parent}
	_ = parent.SetReadDeadline(time.Now().Add(120 * time.Second))
	if err = w.send(packet{Type: "start", Origin: c.Origin, Token: c.Token, Country: c.Country, WantMesh: true}); err != nil {
		reason := queuedStartupFailure(parent)
		parent.Close()
		if reason != nil {
			return nil, reason
		}
		return nil, launchFailure(processDone)
	}
	initial, err := receive(parent)
	if reason := startupPacketFailure(initial); err == nil && reason != nil {
		parent.Close()
		return nil, reason
	}
	if err != nil || initial.Type != "accepted" || initial.ID != 0 || initial.Status != nil || initial.Error != "" || initial.Code != "" || initial.Origin != "" || initial.Token != "" || initial.Country != "" || initial.WantMesh {
		var reason error
		if err != nil {
			reason = queuedStartupFailure(parent)
		}
		parent.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if reason != nil {
			return nil, reason
		}
		if err == nil {
			return nil, failure("helper_protocol_failed")
		}
		return nil, launchFailure(processDone)
	}
	_ = parent.SetReadDeadline(time.Time{})
	s := &Session{conn: parent, writer: writer{conn: parent}, done: make(chan struct{}), readDone: make(chan struct{}), pending: make(map[uint64]chan packet), callbacks: make(chan Status, 1), closed: make(chan struct{}), status: Status{State: "preparing", DesiredCountry: c.Country}}
	go s.readLoop()
	callback := c.OnStatus
	if callback != nil {
		go func() {
			for {
				select {
				case value := <-s.callbacks:
					callback(value)
				case <-s.done:
					return
				}
			}
		}()
		keepLatest(s.callbacks, s.Status())
	}
	go func() {
		err := <-processDone
		// The exited helper may have queued its final typed failure before EOF.
		// Drain it before closing our endpoint, bounded even if another inherited
		// descriptor unexpectedly keeps the socket alive.
		_ = parent.SetReadDeadline(time.Now().Add(time.Second))
		<-s.readDone
		_ = parent.Close()
		s.mu.Lock()
		if err != nil && s.err == nil {
			s.err = errors.New("tinc helper stopped unexpectedly")
		}
		s.mu.Unlock()
		lifecycle.Lock()
		lifecycle.active = false
		lifecycle.Unlock()
		close(s.done)
	}()
	go func() {
		select {
		case <-ctx.Done():
			_ = s.Close()
		case <-s.done:
		}
	}()
	success = true
	return s, nil
}

func (s *Session) readLoop() {
	defer close(s.readDone)
	defer s.conn.Close()
	for {
		p, err := receive(s.conn)
		if err != nil {
			return
		}
		if p.Token != "" || p.Origin != "" || p.Country != "" || p.WantMesh {
			return
		}
		switch p.Type {
		case "status":
			if p.Status == nil || p.ID != 0 || p.Error != "" || p.Code != "" {
				return
			}
			s.mu.Lock()
			s.status = copyStatus(*p.Status)
			s.mu.Unlock()
			keepLatest(s.callbacks, *p.Status)
		case "country", "stopped":
			if p.ID == 0 || p.Status != nil || p.Code != "" {
				return
			}
			s.mu.RLock()
			response := s.pending[p.ID]
			s.mu.RUnlock()
			if response != nil {
				select {
				case response <- p:
				default:
					return
				}
			}
		case "error":
			code := p.Code
			if code == "" {
				code = "network_setup_failed"
			}
			if p.ID != 0 || p.Status != nil || !KnownFailure(code) {
				code = "helper_protocol_failed"
			}
			s.mu.Lock()
			s.err = failure(code)
			s.mu.Unlock()
			status := s.Status()
			status.State = "error"
			status.Code = code
			status.Message = "Tinc network setup or cleanup failed"
			s.mu.Lock()
			s.status = status
			s.mu.Unlock()
			keepLatest(s.callbacks, status)
			return
		default:
			return
		}
	}
}

func (s *Session) Status() Status        { s.mu.RLock(); defer s.mu.RUnlock(); return copyStatus(s.status) }
func (s *Session) Done() <-chan struct{} { return s.done }

func (s *Session) request(ctx context.Context, kind, country string) error {
	s.requestMu.Lock()
	defer s.requestMu.Unlock()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	select {
	case <-s.done:
		return errors.New("tinc helper is stopped")
	default:
	}
	s.nextID++
	id := s.nextID
	response := make(chan packet, 1)
	s.mu.Lock()
	s.pending[id] = response
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.pending, id); s.mu.Unlock() }()
	if err := s.writer.send(packet{Type: kind, Country: country, ID: id}); err != nil {
		return err
	}
	select {
	case reply := <-response:
		if reply.ID != id || (kind == "stop" && reply.Type != "stopped") || (kind == "country" && reply.Type != "country") {
			s.conn.Close()
			return errors.New("invalid tinc control response")
		}
		if reply.Error != "" {
			return errors.New("tinc country change is not ready")
		}
		return nil
	case <-s.readDone:
		return errors.New("tinc helper channel closed")
	case <-ctx.Done():
		// Late replies are discarded by ID; a UI deadline does not restore the
		// physical network or cancel the root lease watchdog.
		return ctx.Err()
	}
}

func (s *Session) Country(ctx context.Context, code string) error {
	if !countryValid(code) {
		return errors.New("country must be two uppercase letters")
	}
	return s.request(ctx, "country", code)
}

// Close waits for the helper to restore network state. Parent cancellation or
// EOF uses the same cleanup; lease expiry alone never calls Close.
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		defer close(s.closed)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		err := s.request(ctx, "stop", "")
		_ = s.conn.Close()
		select {
		case <-s.done:
		case <-ctx.Done():
			s.closeErr = failure("cleanup_failed")
			return
		}
		s.mu.RLock()
		runErr := s.err
		s.mu.RUnlock()
		if runErr != nil {
			s.closeErr = runErr
		} else if err != nil && ctx.Err() != nil {
			s.closeErr = failure("cleanup_failed")
		}
	})
	<-s.closed
	return s.closeErr
}
