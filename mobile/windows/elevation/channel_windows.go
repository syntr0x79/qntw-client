package elevation

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/ipc/namedpipe"
)

var shellExecuteEx = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

type shellExecuteInfo struct {
	Size                              uint32
	Mask                              uint32
	Window                            windows.Handle
	Verb, File, Parameters, Directory *uint16
	Show                              int32
	Instance                          windows.Handle
	IDList                            uintptr
	Class                             *uint16
	ClassKey                          windows.Handle
	HotKey                            uint32
	Icon                              windows.Handle
	Process                           windows.Handle
}

func elevate(path, args string) (windows.Handle, error) {
	verb, _ := windows.UTF16PtrFromString("runas")
	file, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return 0, e
	}
	params, e := windows.UTF16PtrFromString(args)
	if e != nil {
		return 0, e
	}
	dir, _ := windows.UTF16PtrFromString(filepath.Dir(path))
	info := shellExecuteInfo{Mask: 0x40 | 0x100, Verb: verb, File: file, Parameters: params, Directory: dir, Show: windows.SW_HIDE}
	info.Size = uint32(unsafe.Sizeof(info))
	r, _, err := shellExecuteEx.Call(uintptr(unsafe.Pointer(&info)))
	runtime.KeepAlive(info)
	if r == 0 {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return 0, errors.New("administrator authorization cancelled")
		}
		return 0, errors.New("administrator helper could not start")
	}
	if info.Process == 0 {
		return 0, errors.New("missing elevated process identity")
	}
	return info.Process, nil
}

type Channel struct {
	net.Conn
	Process     windows.Handle
	processDone chan struct{}
	once        sync.Once
}

func (c *Channel) Close() error {
	var e error
	c.once.Do(func() { e = c.Conn.Close(); go func() { <-c.processDone; windows.CloseHandle(c.Process) }() })
	return e
}
func (c *Channel) Done() <-chan struct{} { return c.processDone }

// ExitError is called before Close, after Done, while this Channel owns the
// retained process handle. Pipe EOF alone never proves successful cleanup.
func (c *Channel) ExitError() error {
	select {
	case <-c.processDone:
	default:
		return errors.New("administrator helper is still running")
	}
	var code uint32
	if err := windows.GetExitCodeProcess(c.Process, &code); err != nil {
		return err
	}
	if code != 0 {
		return errors.New("administrator helper failed during setup or cleanup")
	}
	return nil
}

// Start creates the private pipe before requesting elevation. No credentials
// enter the command line. Both peers authenticate the exact process ID; the
// elevated client opens the pipe with SECURITY_ANONYMOUS in upstream namedpipe,
// so a pipe server can never impersonate its administrator token.
func Start(ctx context.Context, mode string) (*Channel, error) {
	paths, e := InstallPaths()
	if e != nil {
		return nil, e
	}
	helper := paths.NetHelper
	if mode == "tinc" {
		helper = paths.TincHelper
	} else if mode != "vpn" {
		return nil, errors.New("invalid helper mode")
	}
	if e = TrustedExecutable(helper); e != nil {
		return nil, e
	}
	sid, e := ProcessSID(uint32(os.Getpid()))
	if e != nil {
		return nil, e
	}
	random := make([]byte, 24)
	if _, e = rand.Read(random); e != nil {
		return nil, e
	}
	nonce := hex.EncodeToString(random)
	name := `\\.\pipe\Quantway.Desktop.` + nonce
	sd, e := windows.SecurityDescriptorFromString("D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;" + sid + ")")
	if e != nil {
		return nil, e
	}
	listener, e := (&namedpipe.ListenConfig{SecurityDescriptor: sd, InputBufferSize: MaxPacket, OutputBufferSize: MaxPacket}).Listen(name)
	if e != nil {
		return nil, e
	}
	defer listener.Close()
	type launchResult struct {
		handle windows.Handle
		err    error
	}
	launched := make(chan launchResult, 1)
	args := "--pipe " + syscall.EscapeArg(name) + " --caller-pid " + fmt.Sprint(os.Getpid()) + " --nonce " + nonce
	// With the Quantway GUI this goes through its elevated broker: UAC is shown
	// once per GUI session instead of on every connection.
	go func() { h, e := launchHelper(ctx, mode, helper, args, nonce); launched <- launchResult{h, e} }()
	var process windows.Handle
	select {
	case result := <-launched:
		if result.err != nil {
			return nil, result.err
		}
		process = result.handle
	case <-ctx.Done():
		go func() {
			r := <-launched
			if r.handle != 0 {
				windows.WaitForSingleObject(r.handle, windows.INFINITE)
				windows.CloseHandle(r.handle)
			}
		}()
		return nil, ctx.Err()
	}
	pid, e := windows.GetProcessId(process)
	if e != nil {
		windows.CloseHandle(process)
		return nil, e
	}
	processDone := make(chan struct{})
	go func() { windows.WaitForSingleObject(process, windows.INFINITE); close(processDone) }()
	success := false
	defer func() {
		if !success {
			go func() { <-processDone; windows.CloseHandle(process) }()
		}
	}()
	deadline, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	type accepted struct {
		conn net.Conn
		err  error
	}
	ready := make(chan accepted, 1)
	go func() { c, e := listener.Accept(); ready <- accepted{c, e} }()
	var conn net.Conn
	select {
	case result := <-ready:
		if result.err != nil {
			return nil, result.err
		}
		conn = result.conn
	case <-deadline.Done():
		listener.Close()
		go func() {
			r := <-ready
			if r.conn != nil {
				r.conn.Close()
			}
		}()
		return nil, deadline.Err()
	case <-processDone:
		listener.Close()
		go func() {
			r := <-ready
			if r.conn != nil {
				r.conn.Close()
			}
		}()
		return nil, errors.New("elevated helper exited")
	}
	ok := false
	defer func() {
		if !ok {
			conn.Close()
		}
	}()
	h, okHandle := conn.(interface{ Handle() windows.Handle })
	if !okHandle {
		return nil, errors.New("pipe identity unavailable")
	}
	var peer uint32
	if e = windows.GetNamedPipeClientProcessId(h.Handle(), &peer); e != nil || peer != pid {
		return nil, errors.New("unexpected elevated peer")
	}
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	greeting, e := Read(conn)
	if e != nil || greeting.Type != "hello" || greeting.Nonce != nonce || greeting.Mode != mode || greeting.Origin != "" || greeting.Token != "" || greeting.Country != "" || len(greeting.Job) > 0 || greeting.Status != nil || greeting.ControlURL != "" || greeting.Code != "" {
		return nil, errors.New("invalid helper greeting")
	}
	_ = conn.SetDeadline(time.Time{})
	success, ok = true, true
	return &Channel{Conn: conn, Process: process, processDone: processDone}, nil
}

// AcceptParent is called only by the installed elevated binary. Caller SID is
// obtained from the authenticated parent process token, never from a packet.
func AcceptParent(ctx context.Context, name string, parentPID uint32, nonce, mode string) (net.Conn, string, error) {
	if parentPID == 0 || len(nonce) != 48 || name != `\\.\pipe\Quantway.Desktop.`+nonce {
		return nil, "", errors.New("invalid private endpoint")
	}
	if _, e := hex.DecodeString(nonce); e != nil {
		return nil, "", errors.New("invalid endpoint nonce")
	}
	paths, e := VerifySelf(mode)
	if e != nil {
		return nil, "", e
	}
	image, e := processImage(parentPID)
	if e != nil || !strings.EqualFold(image, filepath.Join(paths.Bin, "quantway-helper.exe")) {
		return nil, "", errors.New("unexpected desktop process")
	}
	if e = TrustedExecutable(image); e != nil {
		return nil, "", e
	}
	sid, e := ProcessSID(parentPID)
	if e != nil {
		return nil, "", e
	}
	expectedOwner, e := windows.StringToSid(sid)
	if e != nil {
		return nil, "", e
	}
	connect, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	conn, e := (&namedpipe.DialConfig{ExpectedOwner: expectedOwner}).DialContext(connect, name)
	if e != nil {
		return nil, "", e
	}
	success := false
	defer func() {
		if !success {
			conn.Close()
		}
	}()
	h, ok := conn.(interface{ Handle() windows.Handle })
	if !ok {
		return nil, "", errors.New("pipe identity unavailable")
	}
	var peer uint32
	if e = windows.GetNamedPipeServerProcessId(h.Handle(), &peer); e != nil || peer != parentPID {
		return nil, "", errors.New("unexpected pipe server")
	}
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if e = (&Writer{To: conn}).Write(Packet{Type: "hello", Nonce: nonce, Mode: mode}); e != nil {
		return nil, "", e
	}
	_ = conn.SetDeadline(time.Time{})
	success = true
	return conn, sid, nil
}
