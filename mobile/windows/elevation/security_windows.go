package elevation

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/google/uuid"
	"golang.org/x/sys/windows"
)

type Paths struct{ Root, Bin, NetHelper, TincHelper, TincDaemon, TincControl string }

var trustedOwners = map[string]bool{"S-1-5-18": true, "S-1-5-32-544": true, "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464": true}

func InstallPaths() (Paths, error) {
	root, e := windows.KnownFolderPath(windows.FOLDERID_ProgramFiles, 0)
	if e != nil {
		return Paths{}, e
	}
	root = filepath.Join(root, "Quantway")
	bin := filepath.Join(root, "bin")
	return Paths{Root: root, Bin: bin, NetHelper: filepath.Join(bin, "quantway-net-helper.exe"), TincHelper: filepath.Join(bin, "quantway-tinc-helper.exe"), TincDaemon: filepath.Join(bin, "tincd.exe"), TincControl: filepath.Join(bin, "tinc.exe")}, nil
}

// Trust is based on the installed ACL and owner, never a caller environment
// variable, PATH lookup, working directory or a writable portable archive.
func TrustedExecutable(path string) error {
	paths, e := InstallPaths()
	if e != nil {
		return e
	}
	if !filepath.IsAbs(path) || !strings.EqualFold(filepath.Dir(path), paths.Bin) {
		return errors.New("helper is not installed")
	}
	for p := path; ; p = filepath.Dir(p) {
		u, e := windows.UTF16PtrFromString(p)
		if e != nil {
			return e
		}
		a, e := windows.GetFileAttributes(u)
		if e != nil || a&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return errors.New("unsafe installed path")
		}
		file := strings.EqualFold(p, path)
		if file && a&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
			return errors.New("helper is not a file")
		}
		if !file && a&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
			return errors.New("unsafe installed directory")
		}
		if e = verifyACL(p, file, false); e != nil {
			return e
		}
		if strings.EqualFold(p, paths.Root) {
			break
		}
		if filepath.Dir(p) == p {
			return errors.New("unexpected installation root")
		}
	}
	// A protected child can still be renamed through DELETE_CHILD on its
	// parent. Inspect ancestry too, allowing only harmless creation rights on
	// volume roots (the standard C:\ ACL permits users to create directories).
	for p := filepath.Dir(paths.Root); ; p = filepath.Dir(p) {
		u, _ := windows.UTF16PtrFromString(p)
		a, e := windows.GetFileAttributes(u)
		if e != nil || a&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || a&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
			return errors.New("unsafe installation ancestor")
		}
		sd, err := windows.GetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			return err
		}
		if err = verifyAncestorDescriptor(sd); err != nil {
			return err
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return nil
}
func verifyACL(path string, file, private bool) error {
	sd, e := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		return e
	}
	return verifyDescriptor(sd, private)
}

const fileDeleteChild = 0x00000040

const installedWriteRights = uint32(windows.GENERIC_ALL | windows.GENERIC_WRITE | windows.WRITE_DAC | windows.WRITE_OWNER | windows.DELETE | windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.FILE_WRITE_EA | windows.FILE_WRITE_ATTRIBUTES | fileDeleteChild)

// Adding a previously absent child is not replacing an existing protected
// component. Forbid deletion, ACL/owner edits and reparse-related writes while
// preserving the normal create-directory permission on a volume root.
const ancestorWriteRights = uint32(windows.GENERIC_ALL | windows.GENERIC_WRITE | windows.WRITE_DAC | windows.WRITE_OWNER | windows.DELETE | windows.FILE_WRITE_EA | windows.FILE_WRITE_ATTRIBUTES | fileDeleteChild)

func verifyDescriptor(sd *windows.SECURITY_DESCRIPTOR, private bool) error {
	return verifyDescriptorMask(sd, private, installedWriteRights)
}
func verifyAncestorDescriptor(sd *windows.SECURITY_DESCRIPTOR) error {
	return verifyDescriptorMask(sd, false, ancestorWriteRights)
}
func verifyDescriptorMask(sd *windows.SECURITY_DESCRIPTOR, private bool, dangerous uint32) error {
	owner, _, e := sd.Owner()
	if e != nil || owner == nil || !trustedOwners[owner.String()] {
		return errors.New("unsafe installed owner")
	}
	acl, _, e := sd.DACL()
	if e != nil || acl == nil {
		return errors.New("missing protected ACL")
	}
	// Every allow ACE is checked conservatively; deny ACEs never widen access.
	// INHERIT_ONLY entries do not apply to the current object.
	for i := uint16(0); i < acl.AceCount; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if e = windows.GetAce(acl, uint32(i), &ace); e != nil {
			return e
		}
		hdr := &ace.Header
		if hdr.AceFlags&windows.INHERIT_ONLY_ACE != 0 || hdr.AceType == windows.ACCESS_DENIED_ACE_TYPE {
			continue
		}
		if hdr.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			return errors.New("unsupported installed access rule")
		}
		sid := (*windows.SID)(unsafe.Add(unsafe.Pointer(ace), unsafe.Sizeof(ace.Header)+unsafe.Sizeof(ace.Mask)))
		if !sid.IsValid() {
			return errors.New("invalid installed access rule")
		}
		if !trustedOwners[sid.String()] && (private || uint32(ace.Mask)&dangerous != 0) {
			return errors.New("installed files are writable outside administrators")
		}
	}
	return nil
}

func ProcessSID(pid uint32) (string, error) {
	h, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if e != nil {
		return "", e
	}
	defer windows.CloseHandle(h)
	var token windows.Token
	if e = windows.OpenProcessToken(h, windows.TOKEN_QUERY, &token); e != nil {
		return "", e
	}
	defer token.Close()
	user, e := token.GetTokenUser()
	if e != nil {
		return "", e
	}
	return user.User.Sid.String(), nil
}
func processImage(pid uint32) (string, error) {
	h, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if e != nil {
		return "", e
	}
	defer windows.CloseHandle(h)
	b := make([]uint16, 32768)
	n := uint32(len(b))
	if e = windows.QueryFullProcessImageName(h, 0, &b[0], &n); e != nil {
		return "", e
	}
	return windows.UTF16ToString(b[:n]), nil
}
func StateDirectory(sid string, device uuid.UUID) (string, error) {
	parsed, e := windows.StringToSid(sid)
	if e != nil || !parsed.IsValid() || device == uuid.Nil {
		return "", errors.New("invalid device owner")
	}
	base, e := windows.KnownFolderPath(windows.FOLDERID_ProgramData, 0)
	if e != nil {
		return "", e
	}
	hash := sha256.Sum256([]byte(sid))
	parts := []string{"Quantway", "Tinc", hex.EncodeToString(hash[:16]), device.String()}
	sd, e := windows.SecurityDescriptorFromString("O:BAG:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if e != nil {
		return "", e
	}
	sa := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	for _, part := range parts {
		base = filepath.Join(base, part)
		u, _ := windows.UTF16PtrFromString(base)
		e = windows.CreateDirectory(u, &sa)
		if e != nil && !errors.Is(e, windows.ERROR_ALREADY_EXISTS) {
			return "", e
		}
		a, e := windows.GetFileAttributes(u)
		if e != nil || a&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 || a&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
			return "", errors.New("unsafe device state directory")
		}
		if e = verifyACL(base, false, true); e != nil {
			return "", e
		}
	}
	return base, nil
}
func VerifySelf(mode string) (Paths, error) {
	p, e := InstallPaths()
	if e != nil {
		return p, e
	}
	expected := p.NetHelper
	if mode == "tinc" {
		expected = p.TincHelper
	}
	self, e := os.Executable()
	if e != nil || !strings.EqualFold(self, expected) {
		return p, errors.New("run the installed helper")
	}
	if e = TrustedExecutable(self); e != nil {
		return p, e
	}
	if !windows.GetCurrentProcessToken().IsElevated() {
		return p, errors.New("administrator authorization required")
	}
	return p, nil
}
