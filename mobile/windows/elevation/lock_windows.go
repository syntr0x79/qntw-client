package elevation

import (
	"errors"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const lockName = `Global\Quantway.Desktop.VPN`

func Acquire() (func(), error) {
	runtime.LockOSThread()
	sd, e := windows.SecurityDescriptorFromString("O:BAG:BAD:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;0x00100000;;;BU)")
	if e != nil {
		runtime.UnlockOSThread()
		return nil, e
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	name, _ := windows.UTF16PtrFromString(lockName)
	h, e := windows.CreateMutex(&sa, false, name)
	if e != nil && !errors.Is(e, windows.ERROR_ALREADY_EXISTS) {
		runtime.UnlockOSThread()
		return nil, e
	}
	if e = trustedLock(h); e != nil {
		windows.CloseHandle(h)
		runtime.UnlockOSThread()
		return nil, e
	}
	state, e := windows.WaitForSingleObject(h, 0)
	if e != nil || state != windows.WAIT_OBJECT_0 && state != windows.WAIT_ABANDONED {
		windows.CloseHandle(h)
		runtime.UnlockOSThread()
		return nil, errors.New("another VPN session is active")
	}
	return func() { windows.ReleaseMutex(h); windows.CloseHandle(h); runtime.UnlockOSThread() }, nil
}
func trustedLock(h windows.Handle) error {
	sd, e := windows.GetSecurityInfo(h, windows.SE_KERNEL_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		return e
	}
	owner, _, e := sd.Owner()
	if e != nil || owner == nil || !trustedOwners[owner.String()] {
		return errors.New("unsafe desktop VPN lock")
	}
	return nil
}

// CheckIdle is safe before install-path verification, including the installer's
// private embedded maintenance binary. It never creates a lock or a VPN.
func CheckIdle() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	name, _ := windows.UTF16PtrFromString(lockName)
	h, e := windows.OpenMutex(windows.SYNCHRONIZE|windows.MUTEX_MODIFY_STATE, false, name)
	if errors.Is(e, windows.ERROR_FILE_NOT_FOUND) {
		return nil
	}
	if e != nil {
		return e
	}
	defer windows.CloseHandle(h)
	if e = trustedLock(h); e != nil {
		return e
	}
	state, e := windows.WaitForSingleObject(h, 0)
	if e != nil {
		return e
	}
	if state != windows.WAIT_OBJECT_0 && state != windows.WAIT_ABANDONED {
		return errors.New("disconnect VPN before installation")
	}
	return windows.ReleaseMutex(h)
}
