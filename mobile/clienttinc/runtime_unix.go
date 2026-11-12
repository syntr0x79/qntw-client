//go:build (linux && !android) || (darwin && !ios)

package clienttinc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	wire "github.com/syntr0x79/qntw-client/internal/clienttincwire"
	"golang.org/x/net/proxy"
	"golang.org/x/sys/unix"
)

type Options struct {
	CredentialsPath string
	// Credentials avoids a credential file for the private desktop helper. It
	// is mutually exclusive with CredentialsPath and is copied before use.
	Credentials *Credentials
	// OnStatus receives public state only. Callers must return promptly.
	OnStatus    func(Status)
	StateDir    string
	TincDaemon  string
	TincControl string
	Country     string
	Interface   string
	SOCKSOnly   bool
	DNSMode     string
	HealthURL   string
}
type controller struct {
	mu              sync.RWMutex
	publishMu       sync.Mutex
	status          Status
	country         chan string
	cancel          context.CancelFunc
	onStatus        func(Status)
	meshInput       *meshInput
	meshObservation *MeshStatus
	meshGeneration  uint64
	meshWake        chan struct{}
}

func (c *controller) set(s Status) {
	c.publishMu.Lock()
	defer c.publishMu.Unlock()
	c.mu.Lock()
	s.Mesh = cloneMesh(c.meshObservation)
	if s.State == "expired" || s.State == "revoked" || s.State == "stopped" {
		s.Mesh, s.Ingresses = nil, nil
	}
	c.status = cloneStatus(s)
	c.mu.Unlock()
	if c.onStatus != nil {
		c.onStatus(cloneStatus(s))
	}
}
func (c *controller) get() Status { c.mu.RLock(); defer c.mu.RUnlock(); return cloneStatus(c.status) }

func optionCredentials(o Options) (Credentials, error) {
	if o.Credentials != nil {
		if o.CredentialsPath != "" {
			return Credentials{}, errors.New("choose one credential source")
		}
		return ValidateCredentials(*o.Credentials)
	}
	return ReadCredentials(o.CredentialsPath)
}

// Run owns all pilot interfaces, routes and processes until explicit shutdown.
// Lease failure stops the data path but preserves fail-closed blocking. This
// initial helper never changes the current UI's configuration or engine.
func Run(ctx context.Context, o Options) (result error) {
	if !safeCountry.MatchString(o.Country) || !filepath.IsAbs(o.StateDir) || !filepath.IsAbs(o.TincDaemon) || !filepath.IsAbs(o.TincControl) {
		return errors.New("absolute paths and a two-letter country are required")
	}
	credentials, e := optionCredentials(o)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(o.StateDir, 0700); e != nil {
		return e
	}
	info, e := os.Lstat(o.StateDir)
	if e != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("state directory must be private (0700)")
	}
	lock, e := os.OpenFile(filepath.Join(o.StateDir, "client.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return e
	}
	defer lock.Close()
	if unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB) != nil {
		return errors.New("pilot already running")
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	if e = os.Remove(filepath.Join(o.StateDir, "control.sock")); e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c := &controller{country: make(chan string, 1), cancel: cancel, onStatus: o.OnStatus}
	c.set(Status{State: "preparing", DesiredCountry: o.Country})
	defer c.set(Status{State: "stopped", DesiredCountry: o.Country})
	listener, e := net.ListenUnix("unix", &net.UnixAddr{Net: "unix", Name: filepath.Join(o.StateDir, "control.sock")})
	if e != nil {
		return errors.New("pilot already running or control path unavailable")
	}
	defer listener.Close()
	if e = os.Chmod(filepath.Join(o.StateDir, "control.sock"), 0600); e != nil {
		return e
	}
	server := &http.Server{Handler: c, ReadHeaderTimeout: 3 * time.Second}
	go server.Serve(listener)
	defer server.Close()
	// Admission is read over the existing connection before touching routes,
	// DNS, TUNs or filters. Capacity waiting must leave ordinary Internet intact.
	api := NewAPI(credentials, nil)
	nativeDir := filepath.Join(o.StateDir, "native")
	public, e := identity(ctx, o.TincControl, nativeDir)
	if e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return RuntimeError{Code: "identity_failed"}
	}
	if e = awaitAPI(ctx, c, func() error { return api.Identity(ctx, public) }); e != nil {
		return e
	}
	if e = awaitAPI(ctx, c, func() error { return api.Country(ctx, o.Country) }); e != nil {
		return e
	}
	if e = awaitAPI(ctx, c, func() error {
		config, err := api.Config(ctx)
		if err != nil {
			return err
		}
		if err = ValidateConfig(config, time.Now()); err != nil {
			return err
		}
		if config.Assignment.Country != o.Country {
			return errors.New("requested country is not ready")
		}
		return nil
	}); e != nil {
		return e
	}
	api.client.CloseIdleConnections()
	if ctx.Err() != nil {
		return nil
	}
	c.set(Status{State: "preparing", DesiredCountry: o.Country})
	n, e := prepareNetwork(ctx, o.Interface, !o.SOCKSOnly, o.DNSMode)
	if e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return e
	}
	defer func() { result = errors.Join(result, n.close()) }()
	api = NewAPI(credentials, n.transport())
	var path *dataPath
	defer func() { c.setMeshInput(nil); path.close() }()
	stopMesh := c.startMeshSampler(ctx, o.TincControl, nativeDir, n.system)
	defer stopMesh()
	var current wire.ClientConfig
	var signature string
	desired := o.Country
	nextFetch := time.Now()
	var nextReadiness time.Time
	var lastAPIError error
	ticker := time.NewTicker(readinessRetryInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return nil
		}
		if path != nil && !time.Now().Before(current.ExpiresAt) {
			c.setMeshInput(nil)
			path.close()
			path = nil
			signature = ""
			nextReadiness = time.Time{}
			c.set(Status{State: "expired", Country: current.Assignment.Country, DesiredCountry: desired, Message: "Permission expired; traffic remains blocked"})
		}
		if path != nil && !path.alive() {
			c.setMeshInput(nil)
			path.close()
			path = nil
			signature = ""
			nextReadiness = time.Time{}
			c.set(Status{State: "reconnecting", Country: current.Assignment.Country, DesiredCountry: desired, Message: "Restoring the tinc path"})
			nextFetch = time.Now()
		}
		if !time.Now().Before(nextFetch) {
			requestCtx, requestCancel := context.WithTimeout(ctx, 12*time.Second)
			if path != nil {
				requestCancel()
				requestCtx, requestCancel = context.WithDeadline(ctx, current.ExpiresAt)
			}
			config, err := api.Config(requestCtx)
			requestCancel()
			if err == nil {
				err = ValidateConfig(config, time.Now())
			}
			if err == nil && config.Assignment.Country != desired {
				err = errors.New("requested country is not ready")
			}
			if err == nil {
				if config.Assignment.ExpiresAt.Before(config.ExpiresAt) {
					config.ExpiresAt = config.Assignment.ExpiresAt
				}
				if path != nil && (config.DeviceID != current.DeviceID || config.Assignment.Version < current.Assignment.Version) {
					err = errors.New("stale assignment")
				}
			}
			if err == nil {
				if Fingerprint(config) != signature || path == nil {
					c.setMeshInput(nil)
					path.close()
					path = nil
					signature = ""
					c.set(readinessStatus(c.get(), config, desired, readinessObservation{message: "Starting the authorized path"}, nil))
					startCtx, startCancel := context.WithDeadline(ctx, config.ExpiresAt)
					path, err = startPath(startCtx, n, o.TincDaemon, nativeDir, public, config)
					startCancel()
					if err == nil {
						signature = Fingerprint(config)
					}
				}
				if err == nil {
					current = config
					nextReadiness = time.Now()
				}
			}
			lastAPIError = err
			if err != nil {
				reportAPIError(c, &path, &signature, desired, err)
			}

			delay := 15 * time.Second
			if path != nil {
				if remaining := time.Until(current.ExpiresAt) / 3; remaining < delay {
					delay = remaining
				}
				if delay < time.Second {
					delay = time.Second
				}
			}
			if path == nil || err != nil {
				delay = retryDelay(err)
			}
			nextFetch = time.Now().Add(delay)
		}
		// A successful configuration authorizes a path, not an already finished
		// handshake. Retry local readiness without fetching/renewing that grant.
		if path != nil && !nextReadiness.IsZero() && !time.Now().Before(nextReadiness) && current.Assignment.Country == desired && len(c.country) == 0 {
			var health func(context.Context) error
			if o.HealthURL != "" {
				health = func(probe context.Context) error { return CheckSOCKS(probe, o.HealthURL) }
			}
			var observation readinessObservation
			var valid bool
			check, checkCancel := context.WithDeadline(ctx, current.ExpiresAt)
			checked, activeHealthError := n.checkActiveHealth(check)
			checkCancel()
			if checked {
				if ctx.Err() != nil || !time.Now().Before(current.ExpiresAt) {
					continue
				}
				if activeHealthError != nil {
					return activeHealthError
				}
				// System HTTPS and DNS prove forwarding even if the native
				// control snapshot temporarily reports no direct ingress.
				observation = readinessObservation{ready: true, ingresses: liveIngresses(ctx, o.TincControl, nativeDir, current)}
				valid = ctx.Err() == nil && time.Now().Before(current.ExpiresAt)
			} else {
				observation, valid = probePathReadiness(ctx, current.ExpiresAt, func(probe context.Context) []string {
					return liveIngresses(probe, o.TincControl, nativeDir, current)
				}, health)
			}
			if !valid || ctx.Err() != nil || !time.Now().Before(current.ExpiresAt) || !path.alive() {
				// Re-enter the owner loop immediately: cancellation returns, expiry
				// closes the path, and native death starts normal reconnection.
				continue
			}
			if observation.ready {
				if err := n.activate(ctx); err != nil {
					return err
				}
			}
			published := len(c.country) == 0
			if published {
				c.set(readinessStatus(c.get(), current, desired, observation, lastAPIError))
				c.setMeshInput(publicMeshInput(current))
			}
			if published && observation.ready {
				nextReadiness = time.Time{}
			} else {
				nextReadiness = time.Now().Add(readinessRetryInterval)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case country := <-c.country:
			requestCtx, done := context.WithTimeout(ctx, 10*time.Second)
			if path != nil {
				done()
				requestCtx, done = context.WithDeadline(ctx, current.ExpiresAt)
			}
			countryErr := api.Country(requestCtx, country)
			if countryErr == nil {
				lastAPIError = nil
				desired = country
				nextFetch = time.Now()
				s := c.get()
				s.DesiredCountry = desired
				s.Message = "Preparing the selected country"
				s.Code, s.RetryAfter = "", 0
				c.set(s)
			} else {
				lastAPIError = countryErr
				var refused APIError
				if path != nil && errors.As(countryErr, &refused) && refused.Status == 503 && refused.Code == "capacity_exhausted" {
					desired = current.Assignment.Country
				}
				reportAPIError(c, &path, &signature, desired, countryErr)
			}

			done()
		case <-ticker.C:
		}
	}
}

func awaitAPI(ctx context.Context, c *controller, call func() error) error {
	for {
		e := call()
		if e == nil {
			s := c.get()
			s.Code, s.RetryAfter, s.Message = "", 0, ""
			c.set(s)
			return nil
		}
		var apiError APIError
		if errors.As(e, &apiError) && apiError.Status >= 400 && apiError.Status < 500 && apiError.Status != 429 {
			return e
		}
		s := c.get()
		s.State = "waiting"
		setAPIReason(&s, e)
		c.set(s)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryDelay(e)):
		}
	}
}

// API refusals cannot renew a grant or change the selected country. Only a
// positive authorization response may replace the running path or its TTL.
func reportAPIError(c *controller, path **dataPath, signature *string, desired string, err error) {
	s := c.get()
	s.DesiredCountry = desired
	setAPIReason(&s, err)
	if *path == nil && s.State != "expired" && s.State != "revoked" {
		s.State = "waiting"
	}
	if s.State == "expired" || s.State == "revoked" {
		s.Code, s.RetryAfter = "", 0
		s.Message = "Permission expired or revoked; traffic remains blocked"
	}
	var e APIError
	if errors.As(err, &e) && accessRevoked(e.Status) {
		c.setMeshInput(nil)
		(*path).close()
		*path = nil
		*signature = ""
		s.State, s.Code, s.RetryAfter = "revoked", "", 0
		if e.Status == 402 {
			s.State = "expired"
		}
		s.Message = "Device access revoked; traffic remains blocked"
	}
	c.set(s)
}

func accessRevoked(status int) bool {
	return status == 401 || status == 402 || status == 403 || status == 410
}

func retryDelay(err error) time.Duration {
	var e APIError
	if errors.As(err, &e) && e.RetryAfter > 0 {
		seconds := e.RetryAfter
		if seconds > 60 {
			seconds = 60
		}
		return time.Duration(seconds) * time.Second
	}
	return 2 * time.Second
}

func setAPIReason(s *Status, err error) {
	s.Code, s.RetryAfter = "", 0
	s.Message = "Server configuration temporarily unavailable"
	var e APIError
	if errors.As(err, &e) {
		s.RetryAfter = int(retryDelay(err) / time.Second)
		if e.Status == 503 && e.Code == "capacity_exhausted" {
			s.Code = "capacity_exhausted"
			s.Message = "Все подключения заняты. Повторите позже."
		}
	}
}

func liveIngresses(ctx context.Context, ctl, dir string, c wire.ClientConfig) []string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	b, e := command(ctx, "", ctl, "-c", dir, "--pidfile="+filepath.Join(dir, "tinc.pid"), "dump", "connections")
	if e != nil {
		return nil
	}
	edges, e := command(ctx, "", ctl, "-c", dir, "--pidfile="+filepath.Join(dir, "tinc.pid"), "dump", "edges")
	if e != nil {
		return nil
	}
	return connectedIngresses(string(b), string(edges), c)
}

func (c *controller) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == "GET" && r.URL.Path == "/status":
		json.NewEncoder(w).Encode(c.get())
	case r.Method == "POST" && r.URL.Path == "/stop":
		w.WriteHeader(202)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		c.cancel()
	case r.Method == "POST" && r.URL.Path == "/country":
		var v struct {
			Country string `json:"country"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&v) != nil || !safeCountry.MatchString(v.Country) {
			http.Error(w, "invalid country", 400)
			return
		}
		select {
		case c.country <- v.Country:
			w.WriteHeader(202)
		default:
			http.Error(w, "a country change is pending", 409)
		}
	default:
		http.NotFound(w, r)
	}
}

// Control talks only to the private local pilot process, never the backend.
func Control(ctx context.Context, stateDir, action, country string) ([]byte, error) {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(stateDir, "control.sock"))
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 5 * time.Second, Transport: transport}
	method, path, body := "GET", "/status", ""
	if action == "stop" {
		method, path = "POST", "/stop"
	} else if action == "country" {
		if !safeCountry.MatchString(country) {
			return nil, errors.New("invalid country")
		}
		method, path, body = "POST", "/country", fmt.Sprintf(`{"country":%q}`, country)
	} else if action != "status" {
		return nil, errors.New("invalid control command")
	}
	r, e := http.NewRequestWithContext(ctx, method, "http://local"+path, strings.NewReader(body))
	if e != nil {
		return nil, e
	}
	res, e := client.Do(r)
	if e != nil {
		return nil, errors.New("pilot is not running")
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("control command returned %d", res.StatusCode)
	}
	var value json.RawMessage
	if action == "status" {
		if e = json.NewDecoder(res.Body).Decode(&value); e != nil {
			return nil, e
		}
		return append(value, '\n'), nil
	}
	return []byte("accepted\n"), nil
}

// CheckSOCKS verifies real authorized TCP/TLS forwarding through the local
// client. It never falls back to a direct request on SOCKS failures.
func CheckSOCKS(ctx context.Context, address string) error {
	d, e := proxy.SOCKS5("tcp", "127.0.0.1:1089", nil, &net.Dialer{Timeout: 5 * time.Second})
	if e != nil {
		return e
	}
	cd, ok := d.(proxy.ContextDialer)
	if !ok {
		return errors.New("SOCKS dialer unavailable")
	}
	transport := &http.Transport{DialContext: cd.DialContext}
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 10 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, e := http.NewRequestWithContext(ctx, "GET", address, nil)
	if e != nil {
		return e
	}
	res, e := client.Do(r)
	if e != nil {
		return errors.New("forwarding check failed")
	}
	res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return errors.New("forwarding check refused")
	}
	return nil
}
