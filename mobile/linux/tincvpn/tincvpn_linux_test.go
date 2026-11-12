//go:build linux && !android

package tincvpn

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/syntr0x79/qntw-client/mobile/clienttinc"
	"golang.org/x/sys/unix"
)

const testToken = "test-device-credential-1234567890"

func pair(t *testing.T) (*net.UnixConn, *net.UnixConn) {
	t.Helper()
	a, child, err := newPair()
	if err != nil {
		t.Fatal(err)
	}
	c, err := net.FileConn(child)
	child.Close()
	if err != nil {
		t.Fatal(err)
	}
	b := c.(*net.UnixConn)
	t.Cleanup(func() { a.Close(); b.Close() })
	return a, b
}

func rootDir(t *testing.T) string {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("root-owned state security test; run in the isolated helper test container")
	}
	dir, err := os.MkdirTemp("/run", "quantway-tinc-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func fixtureOptions(t *testing.T) serverOptions {
	t.Helper()
	return serverOptions{
		stateRoot: filepath.Join(rootDir(t), "users"),
		verify:    func() error { return nil },
		lock:      func() (*os.File, error) { return os.Open("/dev/null") },
		device: func(context.Context, clienttinc.Credentials) (uuid.UUID, error) {
			return uuid.MustParse("12345678-1111-4444-8888-123456789abc"), nil
		},
		run: func(ctx context.Context, o clienttinc.Options) error {
			o.OnStatus(Status{State: "connected", Country: o.Country, DesiredCountry: o.Country})
			<-ctx.Done()
			return nil
		},
		country: func(context.Context, string, string) error { return nil },
	}
}

func launchFixture(t *testing.T, o serverOptions) func(*os.File) (<-chan error, error) {
	t.Helper()
	return func(child *os.File) (<-chan error, error) {
		c, err := net.FileConn(child)
		if err != nil {
			return nil, err
		}
		done := make(chan error, 1)
		go func() { done <- serve(context.Background(), c.(*net.UnixConn), uint32(os.Getuid()), o); close(done) }()
		return done, nil
	}
}

func awaitState(t *testing.T, ch <-chan Status, state string) Status {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		select {
		case s := <-ch:
			if s.State == state {
				return s
			}
		case <-timer.C:
			t.Fatalf("did not receive state %q", state)
			return Status{}
		}
	}
}

func TestSessionExpiryCountryAndStopWaitForCleanup(t *testing.T) {
	o := fixtureOptions(t)
	expire := make(chan struct{})
	cleanupStarted, allowCleanup := make(chan struct{}), make(chan struct{})
	var runCancelled atomic.Bool
	o.run = func(ctx context.Context, opts clienttinc.Options) error {
		if opts.CredentialsPath != "" || opts.Credentials == nil || opts.Credentials.Token != testToken || opts.TincDaemon != NativeDaemon || opts.TincControl != NativeControl || opts.DNSMode != "resolved" || opts.SOCKSOnly {
			return errors.New("unsafe runtime options")
		}
		opts.OnStatus(Status{State: "connected", Country: opts.Country})
		select {
		case <-expire:
			opts.OnStatus(Status{State: "expired", Message: "Permission expired; traffic remains blocked"})
		case <-ctx.Done():
		}
		<-ctx.Done()
		runCancelled.Store(true)
		close(cleanupStarted)
		<-allowCleanup
		return nil
	}
	var gotCountry string
	var countryMu sync.Mutex
	o.country = func(_ context.Context, state, code string) error {
		countryMu.Lock()
		gotCountry = code
		countryMu.Unlock()
		return nil
	}
	states := make(chan Status, 10)
	s, err := Start(context.Background(), Config{Origin: apiOrigin, Token: testToken, Country: "NL", OnStatus: func(v Status) { states <- v }, launch: launchFixture(t, o)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		select {
		case <-allowCleanup:
		default:
			close(allowCleanup)
		}
		s.Close()
	})
	awaitState(t, states, "connected")
	if _, err := Start(context.Background(), Config{Origin: apiOrigin, Token: testToken, Country: "NL"}); err == nil {
		t.Fatal("second session accepted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err = s.Country(ctx, "DE")
	countryMu.Lock()
	selectedCountry := gotCountry
	countryMu.Unlock()
	if err != nil || selectedCountry != "DE" {
		t.Fatalf("country control: %v %q", err, selectedCountry)
	}
	if err = s.Country(ctx, "../"); err == nil {
		t.Fatal("invalid country accepted")
	}
	close(expire)
	awaitState(t, states, "expired")
	if runCancelled.Load() {
		t.Fatal("lease expiry restored physical networking")
	}
	select {
	case <-s.Done():
		t.Fatal("expired helper exited")
	default:
	}
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	select {
	case <-cleanupStarted:
	case <-time.After(time.Second):
		t.Fatal("stop did not cancel runtime")
	}
	select {
	case err := <-closed:
		t.Fatalf("stop returned before cleanup: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(allowCleanup)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stop did not acknowledge cleanup")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestParentEOFAndCancellationCleanRuntime(t *testing.T) {
	for _, mode := range []string{"eof", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			o := fixtureOptions(t)
			started, cleaned := make(chan struct{}), make(chan struct{})
			o.run = func(ctx context.Context, opts clienttinc.Options) error {
				close(started)
				<-ctx.Done()
				close(cleaned)
				return nil
			}
			a, b := pair(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- serve(ctx, b, uint32(os.Getuid()), o) }()
			if err := (&writer{conn: a}).send(packet{Type: "start", Origin: apiOrigin, Token: testToken, Country: "NL"}); err != nil {
				t.Fatal(err)
			}
			p, err := receive(a)
			if err != nil || p.Type != "accepted" {
				t.Fatalf("start: %v %v", p.Type, err)
			}
			<-started
			if mode == "eof" {
				a.Close()
			} else {
				cancel()
			}
			select {
			case <-cleaned:
			case <-time.After(time.Second):
				t.Fatal("parent loss did not clean runtime")
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("helper did not finish")
			}
		})
	}
}

func TestCountryDeadlineDoesNotDropVPNOrConfuseLaterReply(t *testing.T) {
	o := fixtureOptions(t)
	var cancelled atomic.Bool
	o.run = func(ctx context.Context, opts clienttinc.Options) error {
		opts.OnStatus(Status{State: "connected"})
		<-ctx.Done()
		cancelled.Store(true)
		return nil
	}
	var calls atomic.Int32
	o.country = func(ctx context.Context, _ string, _ string) error {
		if calls.Add(1) == 1 {
			select {
			case <-time.After(100 * time.Millisecond):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	states := make(chan Status, 4)
	s, err := Start(context.Background(), Config{Origin: apiOrigin, Token: testToken, Country: "NL", OnStatus: func(s Status) { states <- s }, launch: launchFixture(t, o)})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	awaitState(t, states, "connected")
	short, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	if err = s.Country(short, "DE"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("country deadline: %v", err)
	}
	cancel()
	if cancelled.Load() {
		t.Fatal("country UI deadline stopped the VPN")
	}
	long, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = s.Country(long, "NL"); err != nil {
		t.Fatalf("late country ACK poisoned next request: %v", err)
	}
	if cancelled.Load() {
		t.Fatal("late country ACK stopped the VPN")
	}
}

func TestAuthoritativeDeviceStateSurvivesTokenRotation(t *testing.T) {
	o := fixtureOptions(t)
	var mu sync.Mutex
	var states []string
	o.run = func(ctx context.Context, opts clienttinc.Options) error {
		mu.Lock()
		states = append(states, opts.StateDir)
		mu.Unlock()
		key := filepath.Join(opts.StateDir, "test-private-key")
		if _, err := os.Stat(key); os.IsNotExist(err) {
			if err = os.WriteFile(key, []byte("persistent-key"), 0600); err != nil {
				return err
			}
		}
		opts.OnStatus(Status{State: "connected"})
		<-ctx.Done()
		return nil
	}
	for _, token := range []string{testToken, "rotated-device-credential-0987654321"} {
		ch := make(chan Status, 4)
		s, err := Start(context.Background(), Config{Origin: apiOrigin, Token: token, Country: "NL", OnStatus: func(s Status) { ch <- s }, launch: launchFixture(t, o)})
		if err != nil {
			t.Fatal(err)
		}
		awaitState(t, ch, "connected")
		if err = s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(states) != 2 || states[0] != states[1] || !strings.HasSuffix(states[0], "/12345678-1111-4444-8888-123456789abc") {
		t.Fatalf("identity rotated: %v", states)
	}
	filepath.WalkDir(o.stateRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			t.Fatal(err)
		}
		fi, err := d.Info()
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm()&0077 != 0 {
			t.Errorf("state not private: %s", path)
		}
		if !d.IsDir() {
			data, _ := os.ReadFile(path)
			if strings.Contains(string(data), testToken) || strings.Contains(string(data), "rotated-device-credential") {
				t.Error("credential persisted")
			}
		}
		return nil
	})
}

func TestRejectsForeignPeerBeforeCredentialsOrState(t *testing.T) {
	a, b := pair(t)
	var invoked atomic.Bool
	err := serve(context.Background(), b, uint32(os.Getuid())+1, serverOptions{verify: func() error { invoked.Store(true); return nil }})
	if err == nil || invoked.Load() {
		t.Fatal("foreign socket creator accepted")
	}
	a.Close()
}

func TestInvalidRequestsNeverAuthorizeOrRun(t *testing.T) {
	base := packet{Type: "start", Origin: apiOrigin, Token: testToken, Country: "NL"}
	var cases [][]byte
	encode := func(p packet) []byte { b, _ := json.Marshal(p); return b }
	p := base
	p.Origin = "https://attacker.example"
	cases = append(cases, encode(p))
	p = base
	p.Origin = "https://qntw.link:444"
	cases = append(cases, encode(p))
	p = base
	p.Token = "short"
	cases = append(cases, encode(p))
	p = base
	p.Country = "../../root"
	cases = append(cases, encode(p))
	p = base
	p.Type = "stop"
	cases = append(cases, encode(p))
	p = base
	p.ID = 1
	cases = append(cases, encode(p))
	p = base
	p.Code = "helper_missing"
	cases = append(cases, encode(p))
	cases = append(cases, []byte(`{"type":"start","origin":"https://qntw.link","token":"`+testToken+`","country":"NL","state_dir":"/etc"}`), []byte(strings.Repeat("a", maxPacket+1)))
	for i, data := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			o := fixtureOptions(t)
			var called atomic.Bool
			o.device = func(context.Context, clienttinc.Credentials) (uuid.UUID, error) {
				called.Store(true)
				return uuid.New(), nil
			}
			a, b := pair(t)
			done := make(chan error, 1)
			go func() { done <- serve(context.Background(), b, uint32(os.Getuid()), o) }()
			if _, err := a.Write(data); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err == nil {
					t.Error("bad request accepted")
				}
			case <-time.After(time.Second):
				t.Fatal("bad request hung helper")
			}
			if called.Load() {
				t.Fatal("bad request reached backend")
			}
			if _, err := os.Stat(o.stateRoot); !os.IsNotExist(err) {
				t.Fatal("bad request created state")
			}
		})
	}
}

func TestPrivateProtocolRejectsDescriptorsAndClosesThem(t *testing.T) {
	a, b := pair(t)
	f, err := os.Open("/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	before, _ := os.ReadDir("/proc/self/fd")
	if _, _, err = a.WriteMsgUnix([]byte(`{"type":"stop","id":1}`), unix.UnixRights(int(f.Fd())), nil); err != nil {
		t.Fatal(err)
	}
	if _, err = receive(b); err == nil {
		t.Fatal("ancillary descriptor accepted")
	}
	after, _ := os.ReadDir("/proc/self/fd")
	if len(after) != len(before) {
		t.Fatal("unexpected received descriptor leaked")
	}
}

func TestTrustedExecutablesAndStateRejectSymlinksAndWritableParents(t *testing.T) {
	dir := rootDir(t)
	bin := filepath.Join(dir, "helper")
	if err := os.WriteFile(bin, []byte("helper"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := trustedExecutable(bin); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "alias")
	os.Symlink(bin, alias)
	if trustedExecutable(alias) == nil {
		t.Fatal("symlink executable trusted")
	}
	os.Chmod(dir, 0777)
	if trustedExecutable(bin) == nil {
		t.Fatal("writable parent trusted")
	}
	os.Chmod(dir, 0700)
	root := filepath.Join(dir, "users")
	os.Symlink(dir, root)
	if _, err := stateDirectory(root, 1000, uuid.New()); err == nil {
		t.Fatal("symlink state root accepted")
	}
	os.Remove(root)
	os.Mkdir(root, 0755)
	if _, err := stateDirectory(root, 1000, uuid.New()); err == nil {
		t.Fatal("public state root accepted")
	}
	os.Chmod(root, 0700)
	if _, err := stateDirectory(root, 1000, uuid.Nil); err == nil {
		t.Fatal("nil backend device accepted")
	}
}

func TestSlowStatusConsumerCannotBlockLeaseAndCleanup(t *testing.T) {
	o := fixtureOptions(t)
	consumerBlocked, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	statesSent := make(chan struct{})
	o.run = func(ctx context.Context, opts clienttinc.Options) error {
		for i := 0; i < 100000; i++ {
			opts.OnStatus(Status{State: "connected", Ingresses: []string{"entry"}})
		}
		close(statesSent)
		<-ctx.Done()
		return nil
	}
	s, err := Start(context.Background(), Config{Origin: apiOrigin, Token: testToken, Country: "NL", OnStatus: func(Status) { once.Do(func() { close(consumerBlocked) }); <-release }, launch: launchFixture(t, o)})
	if err != nil {
		t.Fatal(err)
	}
	defer close(release)
	select {
	case <-consumerBlocked:
	case <-time.After(time.Second):
		t.Fatal("callback not started")
	}
	select {
	case <-statesSent:
	case <-time.After(3 * time.Second):
		t.Fatal("slow callback blocked runtime")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
}
