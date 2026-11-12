"""Current-user Windows Credential Manager access, loaded only on Windows.

Only a single app-scoped generic credential is accessed. Cookies never enter
command-line arguments, the registry or a plaintext fallback file.
"""
import ctypes

TARGET = "link.quantway.desktop/account-session/v1"
CRED_TYPE_GENERIC = 1
CRED_PERSIST_LOCAL_MACHINE = 2
MAX_BLOB_BYTES = 5 * 512
ERROR_NOT_FOUND = 1168
DWORD = ctypes.c_uint32
LPBYTE = ctypes.POINTER(ctypes.c_ubyte)


class FILETIME(ctypes.Structure):
    _fields_ = [("low", DWORD), ("high", DWORD)]


class CREDENTIALW(ctypes.Structure):
    _fields_ = [("Flags", DWORD), ("Type", DWORD), ("TargetName", ctypes.c_wchar_p),
                ("Comment", ctypes.c_wchar_p), ("LastWritten", FILETIME),
                ("CredentialBlobSize", DWORD), ("CredentialBlob", LPBYTE),
                ("Persist", DWORD), ("AttributeCount", DWORD), ("Attributes", ctypes.c_void_p),
                ("TargetAlias", ctypes.c_wchar_p), ("UserName", ctypes.c_wchar_p)]


class WindowsCredentialVault:
    def __init__(self, api=None):
        # Injection permits offline ABI tests without touching a user's vault.
        if api is None:
            api = ctypes.WinDLL("advapi32.dll", use_last_error=True)
            api.CredReadW.argtypes = [ctypes.c_wchar_p, DWORD, DWORD,
                                     ctypes.POINTER(ctypes.POINTER(CREDENTIALW))]
            api.CredReadW.restype = ctypes.c_int
            api.CredWriteW.argtypes = [ctypes.POINTER(CREDENTIALW), DWORD]
            api.CredWriteW.restype = ctypes.c_int
            api.CredDeleteW.argtypes = [ctypes.c_wchar_p, DWORD, DWORD]
            api.CredDeleteW.restype = ctypes.c_int
            api.CredFree.argtypes = [ctypes.c_void_p]
            api.CredFree.restype = None
        self.api = api

    def _error(self):
        # Expose a code, never a returned credential, in exception text.
        code = ctypes.get_last_error()
        return OSError(code, "Windows Credential Manager operation failed")

    def read(self):
        pointer = ctypes.POINTER(CREDENTIALW)()
        if not self.api.CredReadW(TARGET, CRED_TYPE_GENERIC, 0, ctypes.byref(pointer)):
            error = self._error()
            if error.errno == ERROR_NOT_FOUND:
                return None
            raise error
        try:
            credential = pointer.contents
            size = credential.CredentialBlobSize
            if credential.Type != CRED_TYPE_GENERIC or credential.TargetName != TARGET or not 0 < size <= MAX_BLOB_BYTES or not credential.CredentialBlob:
                raise OSError("Invalid Quantway credential")
            return ctypes.string_at(credential.CredentialBlob, size)
        finally:
            self.api.CredFree(pointer)

    def write(self, raw):
        if not isinstance(raw, bytes) or not 0 < len(raw) <= MAX_BLOB_BYTES:
            raise OSError("Invalid Quantway credential size")
        buffer = (ctypes.c_ubyte * len(raw)).from_buffer_copy(raw)
        credential = CREDENTIALW()
        credential.Type = CRED_TYPE_GENERIC
        credential.TargetName = TARGET
        credential.Comment = "Quantway — вход в аккаунт"
        credential.CredentialBlobSize = len(raw)
        credential.CredentialBlob = ctypes.cast(buffer, LPBYTE)
        credential.Persist = CRED_PERSIST_LOCAL_MACHINE
        try:
            if not self.api.CredWriteW(ctypes.byref(credential), 0):
                raise self._error()
        finally:
            ctypes.memset(buffer, 0, len(raw))

    def delete(self):
        if not self.api.CredDeleteW(TARGET, CRED_TYPE_GENERIC, 0):
            error = self._error()
            if error.errno != ERROR_NOT_FOUND:
                raise error
