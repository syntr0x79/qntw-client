//go:build windows

// Package wfp owns a temporary Windows Filtering Platform session. Closing the
// session removes only its own filters. Filter changes use WFP transactions.
package wfp

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type baseObjects struct{ provider, filters windows.GUID }
type wfpObjectInstaller func(uintptr) error

type Session struct {
	mu      sync.Mutex
	handle  uintptr
	objects *baseObjects
	tunIDs  []uint64
	peerIDs []uint64
	native  string
}

// Open blocks ordinary IPv4 and IPv6 traffic while permitting loopback,
// DHCP/NDP and this elevated supervisor. Native tinc receives no exception
// until SetPeers supplies the validated ingress addresses.
func Open(native string) (s *Session, err error) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		return nil, errors.New("administrator authorization required")
	}
	if native != "" && !filepath.IsAbs(native) {
		return nil, errors.New("native executable must be an absolute trusted path")
	}
	display, err := createWtFwpmDisplayData0("Quantway", "Temporary VPN protection")
	if err != nil {
		return nil, err
	}
	config := wtFwpmSession0{displayData: *display, flags: cFWPM_SESSION_FLAG_DYNAMIC, txnWaitTimeoutInMSec: 5000}
	s = &Session{native: native}
	if err = fwpmEngineOpen0(nil, cRPC_C_AUTHN_WINNT, nil, &config, unsafe.Pointer(&s.handle)); err != nil {
		return nil, err
	}
	defer func(candidate *Session) {
		if err != nil {
			_ = candidate.Close()
		}
	}(s)
	err = runTransaction(s.handle, func(h uintptr) error {
		var e error
		s.objects, e = registerObjects(h)
		if e != nil {
			return e
		}
		self, e := os.Executable()
		if e != nil {
			return e
		}
		if _, e = s.permitProgram(self, nil); e != nil {
			return e
		}
		for _, install := range []func(uintptr, *baseObjects, uint8) error{permitLoopback, permitDHCPIPv4, permitDHCPIPv6, permitNdp} {
			if e = install(h, s.objects, 12); e != nil {
				return e
			}
		}
		return blockAll(h, s.objects, 0)
	})
	if err != nil {
		return nil, fmt.Errorf("enable VPN protection: %w", err)
	}
	return s, nil
}

func registerObjects(h uintptr) (*baseObjects, error) {
	b := &baseObjects{}
	var err error
	if b.provider, err = windows.GenerateGUID(); err != nil {
		return nil, err
	}
	if b.filters, err = windows.GenerateGUID(); err != nil {
		return nil, err
	}
	d, err := createWtFwpmDisplayData0("Quantway", "Temporary VPN protection")
	if err != nil {
		return nil, err
	}
	provider := wtFwpmProvider0{providerKey: b.provider, displayData: *d}
	if err = fwpmProviderAdd0(h, &provider, 0); err != nil {
		return nil, err
	}
	sublayer := wtFwpmSublayer0{subLayerKey: b.filters, displayData: *d, providerKey: &b.provider, weight: ^uint16(0)}
	if err = fwpmSubLayerAdd0(h, &sublayer, 0); err != nil {
		return nil, err
	}
	return b, nil
}

func runTransaction(h uintptr, install wfpObjectInstaller) error {
	if err := fwpmTransactionBegin0(h, 0); err != nil {
		return err
	}
	if err := install(h); err != nil {
		_ = fwpmTransactionAbort0(h)
		return err
	}
	if err := fwpmTransactionCommit0(h); err != nil {
		_ = fwpmTransactionAbort0(h)
		return err
	}
	return nil
}

func createWtFwpmDisplayData0(name, description string) (*wtFwpmDisplayData0, error) {
	n, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	d, err := windows.UTF16PtrFromString(description)
	if err != nil {
		return nil, err
	}
	return &wtFwpmDisplayData0{name: n, description: d}, nil
}
func filterWeight(w uint8) wtFwpValue0 { return wtFwpValue0{_type: cFWP_UINT8, value: uintptr(w)} }
func wrapErr(err error) error          { return err }

func (s *Session) add(label string, layer windows.GUID, conditions []wtFwpmFilterCondition0, weight uint8) (uint64, error) {
	d, err := createWtFwpmDisplayData0("Quantway "+label, "")
	if err != nil {
		return 0, err
	}
	f := wtFwpmFilter0{displayData: *d, providerKey: &s.objects.provider, subLayerKey: s.objects.filters,
		layerKey: layer, weight: filterWeight(weight), action: wtFwpmAction0{_type: cFWP_ACTION_PERMIT},
		numFilterConditions: uint32(len(conditions))}
	if len(conditions) > 0 {
		f.filterCondition = &conditions[0]
	}
	var id uint64
	err = fwpmFilterAdd0(s.handle, &f, 0, &id)
	runtime.KeepAlive(conditions)
	return id, err
}

func (s *Session) permitProgram(path string, peer *netip.AddrPort) ([]uint64, error) {
	w, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	var app *wtFwpByteBlob
	if err = fwpmGetAppIdFromFileName0(w, unsafe.Pointer(&app)); err != nil {
		return nil, err
	}
	defer fwpmFreeMemory0(unsafe.Pointer(&app))
	// The immutable installed executable must also run with the Administrators
	// group enabled. A normal unelevated launch of the same file does not match.
	sd, err := windows.SecurityDescriptorFromString("D:(A;;CC;;;BA)")
	if err != nil {
		return nil, err
	}
	blob := wtFwpByteBlob{size: sd.Length(), data: (*byte)(unsafe.Pointer(sd))}
	// WFP unions encode pointer values as uintptr. Pin the referenced Go
	// allocations while helper calls may grow and relocate a goroutine stack.
	var pinned runtime.Pinner
	defer pinned.Unpin()
	pinned.Pin(sd)
	pinned.Pin(&blob)
	conditions := []wtFwpmFilterCondition0{
		{fieldKey: cFWPM_CONDITION_ALE_APP_ID, matchType: cFWP_MATCH_EQUAL, conditionValue: wtFwpConditionValue0{_type: cFWP_BYTE_BLOB_TYPE, value: uintptr(unsafe.Pointer(app))}},
		{fieldKey: cFWPM_CONDITION_ALE_USER_ID, matchType: cFWP_MATCH_EQUAL, conditionValue: wtFwpConditionValue0{_type: cFWP_SECURITY_DESCRIPTOR_TYPE, value: uintptr(unsafe.Pointer(&blob))}},
	}
	var mask wtFwpV4AddrAndMask
	pinned.Pin(&mask)
	if peer != nil {
		if !peer.IsValid() || !peer.Addr().Is4() || peer.Port() == 0 {
			return nil, errors.New("invalid native ingress endpoint")
		}
		v := peer.Addr().As4()
		mask = wtFwpV4AddrAndMask{addr: binary.BigEndian.Uint32(v[:]), mask: ^uint32(0)}
		conditions = append(conditions,
			wtFwpmFilterCondition0{fieldKey: cFWPM_CONDITION_IP_REMOTE_ADDRESS, matchType: cFWP_MATCH_EQUAL, conditionValue: wtFwpConditionValue0{_type: cFWP_V4_ADDR_MASK, value: uintptr(unsafe.Pointer(&mask))}},
			wtFwpmFilterCondition0{fieldKey: cFWPM_CONDITION_IP_REMOTE_PORT, matchType: cFWP_MATCH_EQUAL, conditionValue: wtFwpConditionValue0{_type: cFWP_UINT16, value: uintptr(peer.Port())}},
			wtFwpmFilterCondition0{fieldKey: cFWPM_CONDITION_IP_PROTOCOL, matchType: cFWP_MATCH_EQUAL, conditionValue: wtFwpConditionValue0{_type: cFWP_UINT8, value: windows.IPPROTO_TCP}},
		)
	}
	id, err := s.add("elevated transport", cFWPM_LAYER_ALE_AUTH_CONNECT_V4, conditions, 15)
	runtime.KeepAlive(sd)
	runtime.KeepAlive(mask)
	runtime.KeepAlive(app)
	if err != nil {
		return nil, err
	}
	return []uint64{id}, nil
}

var deleteFilter = modfwpuclnt.NewProc("FwpmFilterDeleteById0")

func removeFilter(h uintptr, id uint64) error {
	// Both supported targets (amd64/arm64) pass the full UINT64 in one slot.
	r, _, _ := syscall.SyscallN(deleteFilter.Addr(), h, uintptr(id))
	if r != 0 {
		return syscall.Errno(r)
	}
	return nil
}

// SetPeers atomically replaces the native executable's TCP endpoint permits.
func (s *Session) SetPeers(peers []netip.AddrPort) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handle == 0 {
		return errors.New("VPN protection closed")
	}
	if len(peers) > 128 || (len(peers) > 0 && s.native == "") {
		return errors.New("invalid native ingress policy")
	}
	var ids []uint64
	err := runTransaction(s.handle, func(h uintptr) error {
		for _, id := range s.peerIDs {
			if e := removeFilter(h, id); e != nil {
				return e
			}
		}
		for _, peer := range peers {
			added, e := s.permitProgram(s.native, &peer)
			if e != nil {
				return e
			}
			ids = append(ids, added...)
		}
		return nil
	})
	if err == nil {
		s.peerIDs = ids
	}
	return err
}

// SetTUN atomically replaces the only interface ordinary programs may use.
// Passing zero disables the data path while retaining the blocking policy.
func (s *Session) SetTUN(luid uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handle == 0 {
		return errors.New("VPN protection closed")
	}
	var ids []uint64
	err := runTransaction(s.handle, func(h uintptr) error {
		for _, id := range s.tunIDs {
			if e := removeFilter(h, id); e != nil {
				return e
			}
		}
		if luid == 0 {
			return nil
		}
		var pinned runtime.Pinner
		defer pinned.Unpin()
		pinned.Pin(&luid)
		conditions := []wtFwpmFilterCondition0{{fieldKey: cFWPM_CONDITION_IP_LOCAL_INTERFACE, matchType: cFWP_MATCH_EQUAL, conditionValue: wtFwpConditionValue0{_type: cFWP_UINT64, value: uintptr(unsafe.Pointer(&luid))}}}
		for _, layer := range []windows.GUID{cFWPM_LAYER_ALE_AUTH_CONNECT_V4, cFWPM_LAYER_ALE_AUTH_RECV_ACCEPT_V4} {
			id, e := s.add("system tunnel", layer, conditions, 13)
			if e != nil {
				return e
			}
			ids = append(ids, id)
		}
		runtime.KeepAlive(luid)
		return nil
	})
	if err == nil {
		s.tunIDs = ids
	}
	return err
}

func (s *Session) Close() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.handle == 0 {
		return nil
	}
	err := fwpmEngineClose0(s.handle)
	if err == nil {
		s.handle = 0
	}
	return err
}
