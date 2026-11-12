# Windows desktop port

The existing Python GTK UI is shared with the Linux client. Native MSYS2 UCRT64
(amd64) and CLANGARM64 (arm64) runtimes are pinned by exact package URLs and
SHA256 in `runtime-lock.json`. The offline NSIS installer includes that private
Python/GTK runtime, static Go helpers, patched tinc, and the unmodified official
signed Wintun DLL. Users do not install Python or MSYS2.

Build on macOS/Linux with Go, Python 3.14 (or Python plus the `zstd` CLI), Git,
Docker and public download access:

```sh
sh mobile/scripts/build-windows-client.sh all
```

Use `amd64` or `arm64` for one target. `QUANTWAY_WINDOWS_BUILD_DIR` and
`QUANTWAY_WINDOWS_OUTPUT_DIR` isolate all intermediate/output files. The
builder downloads packages into the private cache, never installs tools on the
host, starts no VM and contacts no application backend. The native cross
compiler is LLVM-MinGW 20261020; source commit and build inputs are recorded.
The Docker image/Ubuntu package repositories are not a historical snapshot:
this records selected inputs without promising future bit-for-bit rebuilding.

An intentional runtime update is separate from normal builds:

```sh
python3 mobile/windows/runtime.py lock --cache /tmp/quantway-windows-runtime --lock mobile/windows/runtime-lock.json
```

The release pipeline must then stage and natively test the updated stack.
Detached MSYS2 GPG signatures are not currently verified by this collector;
transport is official HTTPS and all packages are checked against the reviewed
lock. The Wintun ZIP has an independently fixed vendor-published SHA256.

Layout under `Program Files\Quantway`:

- `quantway.exe` — ordinary-user, no-console launcher;
- `runtime/` — Python, GI, GTK, icons, schemas and dependency licenses;
- `ui/`, `assets/` — existing application screens and branding;
- `bin/` — fixed helper/native executable paths and `wintun.dll`;
- `licenses/` — Go notices, Wintun license, full patched tinc source and locks.

The bootstrap runs Python in isolated mode, sets a fixed installed helper path,
and adds only the bundled DLL/typelib paths. User image-loader caches are
created in LocalAppData, not Program Files. The loader cache is generated from
bundled metadata tied to the runtime lock and architecture DLL hashes. Normal
GUI startup does not enumerate DLLs or launch the loader query; incomplete
developer bundles use a bounded, nonfatal query fallback. Account secrets use Windows
Credential Manager; privileged keys/state are in ProgramData. The installer
runs as administrator, but does not start the GUI elevated or connect a VPN.
The install root has a protected SYSTEM/Administrators full-control and Users
read/execute DACL. Child directories/files inherit that root DACL; their owner
is Administrators. Inheritable grants are never recursively applied directly
to files. Updates/uninstall check
the common desktop VPN lock using the contained fresh helper; keys are retained.

The native tinc backend uses Wintun only, router mode, `Interface=qtm0` and a
fixed Quantway adapter GUID. IPv4 raw packets get a synthetic Ethernet header
for tinc. Public sockets are pinned to the physical IPv4 interface from the
trusted `QUANTWAY_PHYSICAL_IFINDEX`; nonnumeric addresses/IPv6/raw sockets are
rejected. The authenticated native loopback TCP control connection has a
loopback-only interface exception. WFP enforcement is owned by the Go session.

Cross-compilation and PE inspection do not prove Windows installation, ACL,
UAC, Wintun or VPN behavior. `BUILD-RESULT.json` deliberately remains
`built_not_yet_native_windows_validated` until actual VM tests are recorded.
Application/installer Authenticode signing requires a release certificate and
is separate from the vendor-signed Wintun driver.

Installer and uninstaller record UTF-16 step/return-code logs in
`%TEMP%\Quantway-Setup-<architecture>-<PID>.log`. Silent (`/S`) failures return
a nonzero exit code and never wait on the installer's own error MessageBox.
The ACL/file-owner passes have bounded external-command timeouts.

The Windows client native fork disables all tinc script hooks at compile time.
The Go supervisor configures the network and passes only Windows API-derived
system directories plus its numeric physical-interface index to native children.
The fixed `tinc stop` path addresses the per-device foreground daemon through its
local control protocol and never installs or removes a Windows service.
