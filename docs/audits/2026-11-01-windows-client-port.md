# Quantway: Windows port, 2026-11-01

## Scope

The existing desktop screens, cabinet, external subscriptions and home/Tinc mesh
workflow are reused from the Python/GTK4 UI. Windows packaging supplies one
coherent Python/PyGObject/GTK runtime from locked MSYS2 packages, the Go helpers,
native tinc and the official signed Wintun distribution. The application and
installer themselves are currently unsigned preview binaries.

Targets: Windows x86_64 and Windows 11 ARM64. ARM64 is a native build rather than
an x86_64 VPN driver running under emulation. The x86 NSIS bootstrap identifies
the native machine with IsWow64Process2; GetNativeSystemInfo alone is unsuitable
for distinguishing all ARM emulation cases. See the [Microsoft documentation](https://learn.microsoft.com/en-us/windows/win32/api/sysinfoapi/nf-sysinfoapi-getnativesysteminfo).

No production services, API deployments, fleet configuration, existing Linux
DEB files or installed Linux files are updated by this port.

## Platform changes

- Common UI adapters select installed `.exe` helpers and assets, use UTF-8 and
  suppress incidental console windows. Windows Credential Manager stores the
  current user's account session; no plaintext fallback is introduced.
- The account API registers Windows devices with Windows platform metadata.
  Existing server contracts and desktop Tinc enrollment are reused.
- The normal user interface requests elevation only for its installed network
  helper. A bounded local named-pipe protocol authenticates both exact process
  IDs, an invocation nonce and the installed protected executable paths. Tokens
  never enter command arguments or temporary credential files. The underlying
  named-pipe library rejects remote clients and uses SECURITY_ANONYMOUS.
- Installed binary owners and ACLs, reparse points and relevant ancestor access
  are checked before elevation. State keys live in an administrator-private
  ProgramData directory scoped to the originating user's SID and backend device.
- Both modes use one global VPN lock. The ordinary mode validates every typed
  outbound before and after elevation, rejecting file/keylog paths, arbitrary
  protobuf plugins, script hooks and unsafe socket policies.
- Wintun, Windows IP Helper and a scoped dynamic WFP session replace Linux TUN,
  nftables, resolved and descriptor passing. Only owned adapters, routes, DNS
  settings and filters are changed. The physical adapter's DNS/default routes
  are preserved. Ordinary physical sockets and the inner assigned Tinc transport
  propagate interface-binding failures instead of falling back to another path.
- Native tinc uses a Windows Wintun packet device, a validated physical interface
  index and guarded sockets. Control loopback connections are treated separately
  from external transport. All ingress connections and the assigned country/exit
  use the existing desktop configuration and authorization checks.
- The Tinc owner retains grant expiry, 500 ms local readiness checks, authenticated
  ingress observations, exit health, per-core socket ownership and bounded UDP
  association lifetimes. Mesh traffic reads the exact owned Windows LUID and
  preserves monotonic session totals across adapter recreation.

## Lifecycle and limits

Stop, EOF, parent-process death and invalid protocol input cancel the elevated
owner. The desktop waits for cleanup and preserves cleanup failures, including
errors joined with cancellation. Blocked GUI output cannot keep the network
owner alive. Native descendants are owned by a Windows Job Object.

The dynamic WFP policy blocks ordinary traffic while the elevated supervisor
lives, including path failure and Tinc permission expiry. Explicit shutdown
removes it after closing the engines. Complete supervisor-process death also
removes its dynamic filters; a crash-persistent kill switch is not claimed.
This follows the documented [WFP dynamic session lifetime](https://learn.microsoft.com/en-us/windows/win32/fwp/object-management).

Initial physical transport is IPv4; application IPv6 is blocked. Native Windows
kernel, driver, UAC, ACL and GUI execution must be distinguished from cross-build
success and host-side protocol tests. No release should be labelled stable based
only on compilation.

## Verification before native VM access

- The existing GTK UI regression suite passed: 142 tests on Linux, including the
  new platform adapter/vault ABI tests. The first QA bus-collision attempt was
  retained; the isolated no-bus Xvfb run passed.
- Host Go race/vet checks passed for shared validation, frame boundaries,
  cancellation, routing rollback, readiness and assignment restrictions.
- Real net.Pipe lifecycle tests cover pending Stop/cleanup failure, blocked
  output, immediate startup failure and nonzero helper exit.
- Windows helper and test binaries cross-compile for AMD64 and ARM64. Native
  tinc/tincd cross-build for both targets. This is not a driver execution result.
- The elevated helper dependency graph excludes mobile/engine in both targets,
  preserving the separate Tinc system dialer. Importing the clienttinc package
  no longer changes Windows' process-wide dialer during package initialization.
- A current Linux ARM64 clienttinc test executable ran successfully in an offline
  unprivileged container. Host engine and generic helper tests also passed.

Private evidence:

- `/private/tmp/quantway-windows-ui-linux-regression-no-bus.log`
- `/private/tmp/quantway-windows-network-review-1123f52256/RESULT.json`
- `/private/tmp/quantway-windows-build-20261101/`
- `/private/tmp/quantway-windows-native-canary-20261101/`

The Windows 11 ARM64 VM is available at 10.211.55.13. Parallels Guest Tools are
installed; this host edition cannot execute guest commands with prlctl exec.
Computer Use rejected access to Windows PowerShell. The user subsequently
enabled a dedicated, host-restricted SSH key by running the checksum-verified
setup/repair scripts. Native offline checks now run through that explicitly
provided connection. No live VPN verification is claimed at this stage.

## Preview `.2` build and pre-bind correction

Preview `.1` is withheld. Final review found that Windows PrepareSocket called
getsockname before bind/connect; Winsock can return WSAEINVAL in that state.
The physical, overlay and system adapters now read SO_PROTOCOL_INFOW before
setting IP_UNICAST_IF. There is no error-to-IPv4 fallback. See Microsoft's
[getsockname contract](https://learn.microsoft.com/en-us/windows/win32/api/winsock2/nf-winsock2-getsockname).

Windows regression executables create fresh TCP and UDP sockets before binding,
check the selected interface, reject IPv6 and propagate closed-handle errors.
They compile for both targets and now pass on the ARM64 VM. An initial native
test failure was an assertion bug: IP_UNICAST_IF SET takes network byte order,
whereas GET returns host byte order. Only the test/canary readback changed; the
product setter was already correct. The failed attempt is preserved. See the
[Microsoft socket option contract](https://learn.microsoft.com/en-us/windows/win32/winsock/ipproto-ip-socket-options).
Original source freezes, installers and reports remain preserved.

| Version `.2` | Installer SHA256 | Size |
| --- | --- | ---: |
| AMD64 | `84e17fba4e617f1b541b1b3414d159cadbd508bdc0021042459e8f226b782804` | 163,887,523 B |
| ARM64 | `b774ba917c3e234b3c1fe23a401adfcd9fae3ba6fc4a2e0d50dcd13fa6e5884b` | 150,149,058 B |

The `.2` rebuild replaces only the three Go helpers in each payload. Native
binaries, Python/GTK runtime, launcher, icon and installer policy are reused.
Source equality and all 471 PE machine/import checks per target pass.
Public artifacts and versioned proofs are in `bin/quantway-windows-client/`.

Additional private evidence:

- `/private/tmp/quantway-windows-prebind-fix-20261101/RESULT.json`
- `/private/tmp/quantway-windows-build-20261101/rebuild-r2.log`
- `/private/tmp/quantway-windows-build-20261101-r2/`
- `/private/tmp/quantway-windows-native-canary-20261101-v2-prebind.zip`
- `/private/tmp/quantway-windows-native-uiqa-v2-n1dhr41w/`

## Native ARM64 checkpoints and installer `.3`

Actual Windows ARM64 offline checks passed with no skips: seven elevation,
four desktop IPC/lifecycle, seven outbound validation and eight network/socket
tests. These ran with an administrator token in SSH Session 0. They do not
prove normal-user GTK, Credential Manager, UAC prompts, Wintun/WFP or live VPN.
The combined checkpoint is
`/private/tmp/quantway-windows-native-uiqa-v2-n1dhr41w/NATIVE-OFFLINE-CHECKPOINT.json`.

Preview `.2` is also withheld after an actual installer failure. Recursive
`icacls /inheritance:r /grant:r` using OI/CI grants left child files with empty
protected DACLs, preventing helper execution. Preview `.3` protects/grants the
directory itself, resets descendants to inherit its ACL, then sets ownership
separately. Silent failures log and return a nonzero status rather than wait
on an invisible Session 0 message box. The product binaries are identical to
`.2`; the installer and version metadata changed.

The `.3` preflight executed successfully on the ARM64 VM: architecture detection,
temporary directory/file ACLs and native helper `--check-idle` all returned
success. It exited before Program Files or registry mutations. Evidence:
`/private/tmp/quantway-windows-installer-debug-20261101/PREFLIGHT-NATIVE.log`.
Full `.3` installation subsequently passed: exit 0, matching installed version
and architecture, nine selected payload hashes and administrator owners, and
unchanged routes/DNS. The setup applied inheritance to 10,038 descendants and
administrator ownership to 10,039 objects without errors. Local evidence:
`/private/tmp/quantway-windows-installer-debug-20261101/INSTALLED-R3/`.

Normal-user Windows ARM64 UI checks also passed in Session 2 with the same SID
as Explorer and no administrator token: Python/GTK/GI, PNG/SVG, clean helper
JSON, a unique disposable Credential Manager target, and offline screen
construction. No real account target was read by that harness. All owned QA
tasks were removed. A separate normal-token open-only probe denied WRITE_DAC
on all 28 selected installed/state paths, including the private Tinc key;
metadata showed administrator ownership. No key contents or ACLs were changed.

## Functional `.3` failure and `.4` corrections

The user could open the installed client and sign in, but an actual Tinc attempt
remained in readiness. Preview `.3` is withheld. A real Xray MUX reproduction
showed that MUX replaces the dial context with context.Background(), losing
core.Instance. The strict Windows assigned dialer therefore rejected the inner
transport. The fix assigns a helper-generated, never-reused lifetime ID through
SocketConfig.Mark, which MUX preserves. The Windows dialer resolves only a live
owner, cross-checks any available core context, enforces the exact assigned
destination/source and rejects revoked, foreign and missing IDs. It never sends
the ID to an OS SO_MARK operation. Host race checks and five actual ARM64 Windows
tests, including real TCP/UDP MUX paths reaching the strict binder, passed.
These tests intentionally stop before real overlay I/O.

Native canary v5 confirmed actual WFP permit/deny behavior, restored external
HTTPS after closing protection, Wintun creation, overlay socket binding and
successful native dump commands. Its overall result remains failed: Windows'
upstream `tinc stop` consults the SCM and directly returns a boolean, producing
exit 1 even after a successful stop. The client fork's correction uses only the
authenticated local control stop and converts its boolean to a conventional
exit code. All canary attempts restored their route/DNS snapshots and left no
QA adapters. The earlier `10054` control rows also occurred after successful
dump commands and do not establish a failed control channel.

The Windows native fork disables script hooks completely, and helper-spawned
native processes receive only Windows-API-derived system paths and the owned
interface index. This removes inherited PATHEXT/COMSPEC script behavior. The
first GUI launch also recorded a 20-second pixbuf-query timeout; the new
bootstrap prepares its private cache from bundled, locked loader metadata.
Actual `.4` installation passed: exit 0, twelve selected installed hashes and
administrator owners verified, routes/DNS unchanged. The normal-user installed
bootstrap created a fresh private cache in 15 ms without a query subprocess;
all fourteen loader hashes matched. This measures bootstrap preparation only,
not complete GUI startup, and does not flush the OS disk cache.

The first `.4` native canary hit its seven-second worker budget before producing
native daemon diagnostics; the cause was not further attributed. Its network
snapshot was restored and the failure is retained. A separate warm repeat
passed all phases, including WFP allow/deny, native Wintun/dump/control-stop,
the Xray blackhole system TUN, owned DNS/routes and complete rollback. This is
local API validation, separate from authenticated exit traffic.

## 2026-11-02: actual traffic and readiness indication

During a sustained authenticated `.4` run, all 46 HTTPS checks, 46 UDP DNS
queries and 46 Windows system DNS checks succeeded over approximately 224
seconds. Every UDP source was `198.18.94.1`, and both owned split routes were
present in every tunnel sample. The user clarified that the indicator changed
while websites continued working. No Windows DNS setting was changed to address
this report. Evidence:
`/private/tmp/quantway-windows-live-check-20261101/DNS-SECOND-READ.stdout.json`.

Windows readiness previously waited for a separate public HTTPS check and could
return to connecting when that check failed. Preview `.5` observes successfully
delivered response payloads from the current assigned outbound, after the
pinned TLS/VLESS path. Local TUN counters and locally generated TCP replies do
not confirm readiness. A real response cancels the pending public check and
confirms that core's forwarding path. Core teardown revokes the observation;
an old writer cannot confirm a replacement assignment. Authorization expiry,
country/assignment checks and native process liveness remain authoritative.
Recent replies provide a bounded 15-second fallback for a transient native
control snapshot failure. When a native ingress remains authenticated, a
confirmed connection is not relabelled merely because the public check failed;
the connection indicator is not an ongoing guarantee for every Internet site.

Host clienttinc race checks and ten actual Windows ARM64 readiness/ownership
tests passed without skips. Both `.5` installers were built from unchanged
frozen sources; only the three Go helpers changed in the payload. Both complete
trees passed the 471-PE architecture/direct-import audit. Native tinc, Wintun,
the UI/runtime, bootstrap and installer policy are unchanged from `.4`.
Actual `.5` installation and its indication/reconnect check remain pending.

Additional evidence:

- `/private/tmp/quantway-windows-native-uiqa-v2-n1dhr41w/UI-NORMAL-PASS-CHECKPOINT.json`
- `/private/tmp/quantway-windows-state-audit-20261101/`
- `/private/tmp/quantway-windows-mux-context-20261101/RESULT.json`
- `/private/tmp/quantway-windows-mux-context-vm-qa-20261101/VM-CLIENTTINC-MUX-ARM64.json`
- `/private/tmp/quantway-windows-canary-system-task-20261101-v5/FIRST-RESULT-READ.stdout.txt`

The temporary SSH setup records prior state and restricts its dedicated public
key and firewall access to the Mac's private bridge address. This access must
be removed after the Windows checks as promised, preserving preexisting access.
