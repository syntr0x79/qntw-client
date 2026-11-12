//go:build darwin

package tun

import (
	"errors"
	"os"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/xtls/xray-core/common/platform"
	"golang.org/x/sys/unix"
)

func quantwayBorrowedPair(t *testing.T) (*os.File, *os.File) {
	t.Helper()
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	owner := os.NewFile(uintptr(pair[0]), "caller-owned-utun")
	peer := os.NewFile(uintptr(pair[1]), "kernel-peer")
	t.Cleanup(func() { owner.Close(); peer.Close() })
	return owner, peer
}

func quantwayFDValid(fd int) error {
	_, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	return err
}

func quantwayMakeAndClose(t *testing.T, fd int) int {
	t.Helper()
	t.Setenv(platform.TunFdKey, strconv.Itoa(fd))
	value, err := NewTun(&Config{Name: "quantway-fd-ownership-test"})
	if err != nil {
		t.Fatal(err)
	}
	d := value.(*DarwinTun)
	copyFD := d.tunFd
	if copyFD == fd {
		t.Error("Xray wrapped the caller descriptor instead of owning a duplicate")
	}
	flags, err := unix.FcntlInt(uintptr(copyFD), unix.F_GETFD, 0)
	if err != nil || flags&unix.FD_CLOEXEC == 0 {
		t.Errorf("Xray duplicate must be CLOEXEC: flags=%d error=%v", flags, err)
	}
	// A supplied interface has caller-managed routes; Start must not attempt
	// host-network setup on this packet socket fixture.
	if err := d.Start(); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := quantwayFDValid(copyFD); !errors.Is(err, unix.EBADF) {
		t.Errorf("Xray-owned duplicate was not closed synchronously: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Errorf("Close must be idempotent: %v", err)
	}
	return copyFD
}

func TestQuantwayBorrowedDarwinTunSurvivesCloseAndGC(t *testing.T) {
	owner, peer := quantwayBorrowedPair(t)
	fd := int(owner.Fd())
	quantwayMakeAndClose(t, fd)
	for i := 0; i < 3; i++ {
		runtime.GC()
		time.Sleep(20 * time.Millisecond)
	}
	if err := quantwayFDValid(fd); err != nil {
		t.Fatalf("Xray finalization closed the caller-owned descriptor: %v", err)
	}
	owner.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := peer.Write([]byte("caller packet after GC")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 128)
	n, err := owner.Read(buf)
	if err != nil || string(buf[:n]) != "caller packet after GC" {
		t.Fatalf("caller descriptor stopped forwarding after Xray Close: %v", err)
	}
	runtime.KeepAlive(owner)
}

func TestQuantwayBorrowedDarwinTunRepeatedAssignmentsAndFDReuse(t *testing.T) {
	for i := 0; i < 32; i++ {
		pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
		if err != nil {
			t.Fatal(err)
		}
		owner := os.NewFile(uintptr(pair[0]), "assignment-utun")
		peer := os.NewFile(uintptr(pair[1]), "assignment-kernel")
		quantwayMakeAndClose(t, pair[0])
		owner.Close()
		peer.Close()
		// Reuse the released low descriptor numbers before collecting the old
		// Xray object. Its cleanup must never close this next assignment.
		next, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_DGRAM, 0)
		if err != nil {
			t.Fatal(err)
		}
		runtime.GC()
		time.Sleep(time.Millisecond)
		for _, fd := range next {
			if err := quantwayFDValid(fd); err != nil {
				t.Errorf("assignment %d: recycled descriptor closed by old Xray: %v", i, err)
			}
			unix.Close(fd)
		}
	}
}

func TestQuantwayDarwinTunRejectsInvalidBorrowedFD(t *testing.T) {
	t.Setenv(platform.TunFdKey, "-1")
	if value, err := NewTun(&Config{Name: "invalid-utun"}); err == nil {
		value.Close()
		t.Fatal("invalid supplied descriptor accepted")
	}
}
