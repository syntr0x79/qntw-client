package elevation

// The broker removes repeated UAC prompts. The desktop GUI owns one elevated
// broker for its whole lifetime: Windows asks for administrator approval once,
// on the first connection. Every VPN session still runs in its own fresh,
// already-elevated helper process (same isolation, cleanup and pipe checks as
// before); the broker only starts that helper without a new UAC prompt.
//
// Trust model:
//   - the broker pipe is owned by BUILTIN\Administrators and its DACL admits
//     only SYSTEM, Administrators and the GUI user's SID;
//   - the client verifies the pipe owner and that the server process is the
//     installed protected quantway-net-helper.exe;
//   - the broker verifies that each client is the installed protected
//     quantway-helper.exe of the same user and that the GUI is the installed
//     protected quantway.exe;
//   - the broker exits together with the GUI process.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.zx2c4.com/wireguard/ipc/namedpipe"
)

// GUIProcessEnv carries the PID of the installed GUI to quantway-helper.exe.
const GUIProcessEnv = "QUANTWAY_GUI_PID"

const administratorsSID = "S-1-5-32-544"

func brokerPipeName(sid string) string {
	h := sha256.Sum256([]byte("quantway-broker:" + sid))
	return `\\.\pipe\Quantway.Broker.` + hex.EncodeToString(h[:16])
}

// GUIPath is the installed desktop executable that may own a broker.
func GUIPath(p Paths) string { return filepath.Join(p.Root, "quantway.exe") }

func brokerGUI() (uint32, bool) {
	value := os.Getenv(GUIProcessEnv)
	if value == "" {
		return 0, false
	}
	pid, e := strconv.ParseUint(value, 10, 32)
	if e != nil || pid == 0 {
		return 0, false
	}
	return uint32(pid), true
}

// launchHelper starts the elevated helper for one session. With a GUI it goes
// through the broker (UAC at most once per GUI lifetime); otherwise it keeps
// the original per-session elevation.
func launchHelper(ctx context.Context, mode, helper, args, nonce string) (windows.Handle, error) {
	gui, ok := brokerGUI()
	if !ok {
		return elevate(helper, args)
	}
	return launchViaBroker(ctx, gui, mode, nonce)
}

func launchViaBroker(ctx context.Context, gui uint32, mode, nonce string) (windows.Handle, error) {
	paths, e := InstallPaths()
	if e != nil {
		return 0, e
	}
	sid, e := ProcessSID(uint32(os.Getpid()))
	if e != nil {
		return 0, e
	}
	name := brokerPipeName(sid)
	conn, e := dialBroker(ctx, name, paths, 2*time.Second)
	if e != nil {
		if conn, e = startBroker(ctx, name, paths, gui); e != nil {
			return 0, e
		}
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	writer := &Writer{To: conn}
	if e = writer.Write(Packet{Type: "launch", Mode: mode, Nonce: nonce}); e != nil {
		return 0, e
	}
	reply, e := Read(conn)
	if e != nil {
		return 0, errors.New("administrator broker did not answer")
	}
	if reply.Type != "launched" || reply.Handle == 0 {
		return 0, errors.New("administrator broker refused the helper")
	}
	handle := windows.Handle(uintptr(reply.Handle))
	// The handle was duplicated into this process by the broker. Verify that it
	// really is the installed helper for the requested mode.
	expected := paths.NetHelper
	if mode == "tinc" {
		expected = paths.TincHelper
	}
	image, e := handleImage(handle)
	if e != nil || !strings.EqualFold(image, expected) {
		windows.CloseHandle(handle)
		return 0, errors.New("unexpected helper from broker")
	}
	return handle, nil
}

func dialBroker(ctx context.Context, name string, paths Paths, timeout time.Duration) (net.Conn, error) {
	owner, e := windows.StringToSid(administratorsSID)
	if e != nil {
		return nil, e
	}
	dial, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, e := (&namedpipe.DialConfig{ExpectedOwner: owner}).DialContext(dial, name)
	if e != nil {
		return nil, e
	}
	h, ok := conn.(interface{ Handle() windows.Handle })
	if !ok {
		conn.Close()
		return nil, errors.New("pipe identity unavailable")
	}
	var server uint32
	if e = windows.GetNamedPipeServerProcessId(h.Handle(), &server); e != nil {
		conn.Close()
		return nil, e
	}
	image, e := processImage(server)
	if e != nil || !strings.EqualFold(image, paths.NetHelper) {
		conn.Close()
		return nil, errors.New("unexpected broker process")
	}
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	hello, e := Read(conn)
	if e != nil || hello.Type != "hello" || hello.Mode != "broker" {
		conn.Close()
		return nil, errors.New("invalid broker greeting")
	}
	_ = conn.SetDeadline(time.Time{})
	return conn, nil
}

// startBroker asks Windows for administrator approval (the only UAC prompt of
// this GUI session) and waits until the broker accepts connections.
func startBroker(ctx context.Context, name string, paths Paths, gui uint32) (net.Conn, error) {
	if e := TrustedExecutable(paths.NetHelper); e != nil {
		return nil, e
	}
	process, e := elevate(paths.NetHelper, "--broker --gui-pid "+fmt.Sprint(gui))
	if e != nil {
		return nil, e
	}
	defer windows.CloseHandle(process)
	deadline := time.Now().Add(25 * time.Second)
	for {
		conn, e := dialBroker(ctx, name, paths, 500*time.Millisecond)
		if e == nil {
			return conn, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if state, _ := windows.WaitForSingleObject(process, 0); state == windows.WAIT_OBJECT_0 {
			// Another broker may have won a concurrent start; try it once more.
			if conn, e2 := dialBroker(ctx, name, paths, time.Second); e2 == nil {
				return conn, nil
			}
			return nil, errors.New("administrator broker exited")
		}
		if time.Now().After(deadline) {
			return nil, errors.New("administrator broker did not start")
		}
		time.Sleep(150 * time.Millisecond)
	}
}

func handleImage(h windows.Handle) (string, error) {
	b := make([]uint16, 32768)
	n := uint32(len(b))
	if e := windows.QueryFullProcessImageName(h, 0, &b[0], &n); e != nil {
		return "", e
	}
	return windows.UTF16ToString(b[:n]), nil
}

// ServeBroker runs in the installed elevated quantway-net-helper.exe until the
// GUI process exits.
func ServeBroker(ctx context.Context, guiPID uint32) error {
	paths, e := VerifySelf("vpn")
	if e != nil {
		return e
	}
	image, e := processImage(guiPID)
	if e != nil || !strings.EqualFold(image, GUIPath(paths)) {
		return errors.New("unexpected desktop process")
	}
	if e = trustedInstalled(image, paths.Root); e != nil {
		return e
	}
	sid, e := ProcessSID(guiPID)
	if e != nil {
		return e
	}
	if _, e = windows.StringToSid(sid); e != nil {
		return e
	}
	gui, e := windows.OpenProcess(windows.SYNCHRONIZE, false, guiPID)
	if e != nil {
		return e
	}
	defer windows.CloseHandle(gui)
	sd, e := windows.SecurityDescriptorFromString("O:BAG:BAD:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GA;;;" + sid + ")")
	if e != nil {
		return e
	}
	listener, e := (&namedpipe.ListenConfig{SecurityDescriptor: sd, InputBufferSize: 4096, OutputBufferSize: 4096}).Listen(brokerPipeName(sid))
	if e != nil {
		return e
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		for {
			state, _ := windows.WaitForSingleObject(gui, 250)
			if state == windows.WAIT_OBJECT_0 || ctx.Err() != nil {
				cancel()
				listener.Close()
				return
			}
		}
	}()
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		conn, e := listener.Accept()
		if e != nil {
			if ctx.Err() != nil {
				return nil
			}
			continue
		}
		wg.Add(1)
		go func() { defer wg.Done(); defer conn.Close(); _ = serveBrokerClient(conn, paths, sid) }()
	}
}

func serveBrokerClient(conn net.Conn, paths Paths, sid string) error {
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	h, ok := conn.(interface{ Handle() windows.Handle })
	if !ok {
		return errors.New("pipe identity unavailable")
	}
	var pid uint32
	if e := windows.GetNamedPipeClientProcessId(h.Handle(), &pid); e != nil {
		return e
	}
	image, e := processImage(pid)
	if e != nil || !strings.EqualFold(image, filepath.Join(paths.Bin, "quantway-helper.exe")) {
		return errors.New("unexpected desktop helper")
	}
	if e = TrustedExecutable(image); e != nil {
		return e
	}
	if caller, e := ProcessSID(pid); e != nil || caller != sid {
		return errors.New("unexpected desktop user")
	}
	writer := &Writer{To: conn}
	if e = writer.Write(Packet{Type: "hello", Mode: "broker"}); e != nil {
		return e
	}
	request, e := Read(conn)
	if e != nil {
		return e
	}
	if request.Type != "launch" || (request.Mode != "vpn" && request.Mode != "tinc") || len(request.Nonce) != 48 || request.Origin != "" || request.Token != "" || len(request.Job) > 0 || request.Handle != 0 {
		return errors.New("invalid broker request")
	}
	if _, e = hex.DecodeString(request.Nonce); e != nil {
		return errors.New("invalid broker nonce")
	}
	helper := paths.NetHelper
	if request.Mode == "tinc" {
		helper = paths.TincHelper
	}
	if e = TrustedExecutable(helper); e != nil {
		return e
	}
	pipe := `\\.\pipe\Quantway.Desktop.` + request.Nonce
	args := "--pipe " + syscall.EscapeArg(pipe) + " --caller-pid " + fmt.Sprint(pid) + " --nonce " + request.Nonce
	child, e := spawnElevated(helper, args, paths.Bin)
	if e != nil {
		_ = writer.Write(Packet{Type: "failed", Code: "helper_start_failed"})
		return e
	}
	defer windows.CloseHandle(child)
	client, e := windows.OpenProcess(windows.PROCESS_DUP_HANDLE, false, pid)
	if e != nil {
		windows.TerminateProcess(child, 1)
		return e
	}
	defer windows.CloseHandle(client)
	var remote windows.Handle
	if e = windows.DuplicateHandle(windows.CurrentProcess(), child, client, &remote, windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, 0); e != nil {
		windows.TerminateProcess(child, 1)
		return e
	}
	return writer.Write(Packet{Type: "launched", Handle: uint64(remote)})
}

func spawnElevated(path, args, dir string) (windows.Handle, error) {
	command, e := windows.UTF16PtrFromString(syscall.EscapeArg(path) + " " + args)
	if e != nil {
		return 0, e
	}
	app, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return 0, e
	}
	directory, e := windows.UTF16PtrFromString(dir)
	if e != nil {
		return 0, e
	}
	si := windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfo{}))}
	var pi windows.ProcessInformation
	const createNoWindow = 0x08000000
	if e = windows.CreateProcess(app, command, nil, nil, false, createNoWindow|windows.CREATE_UNICODE_ENVIRONMENT, nil, directory, &si, &pi); e != nil {
		return 0, e
	}
	windows.CloseHandle(pi.Thread)
	return pi.Process, nil
}
