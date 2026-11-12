# macOS desktop client and Tinc verification

Date: 2026-11-03. Local Apple Silicon build: `bin/quantway-macos-client/Quantway.app`.

**Latest package: 0.3.0 integrates the second designer delivery and passes UI/local package checks. No live VPN was started for the UI update.**

**Last live Tinc acceptance: 0.2.11 passed two live macOS Tinc system-traffic sessions after fixing the packet bridge ENOBUFS failure. The client is disconnected and the original route/DNS are restored.** This is local Tinc acceptance, not a claim of live testing every secondary action or ordinary Xray/AWG mode.

## Implemented

The four supplied desktop concept screens are bundled locally with their inline geometry, icons, palette and Manrope. The shell is AppKit/WebKit with a private native bridge. Account credentials stay in the native process/Keychain. The existing desktop account, subscription, probe and billing APIs are reused through the shared Go helper.

The macOS network supervisor implements `utun`, IPv4/IPv6 split routes, temporary SystemConfiguration DNS, an owned PF anchor and graceful cleanup. Tinc uses the pinned native client, packet-FD framing adapter, shared authorization/lease supervisor, owned inner VLESS sockets and real ingress observations. Native hooks are disabled in the packaged build. Session stop, application EOF and process signals cancel the elevated operation.

## Observed during local testing

Earlier builds established the Tinc path but did not deliver normal system Internet traffic. The first implementation also switched global routes before completing the handshake, interrupting Internet during an indefinite wait. That behavior was corrected: initial handshake/HTTPS verification through SOCKS precedes global activation, and startup has a 45-second limit.

The system had another VPN's supplemental DNS resolver. The first DNS serialization used string values for the intended resolver priority; it was changed to numeric `SearchOrder` and an explicit supplemental default domain.

The readiness check now separately verifies the actual system route, HTTPS through the system TUN using an ordinary socket, UDP DNS through the TUN and macOS resolver selection. Version 0.2.2 correctly refused to report success when the system HTTPS check failed and automatically ended the session.

Version 0.2.3 additionally normalizes the elevated helper's real/effective root identity before using UID-based PF rules and records public connection diagnostics in `~/Library/Application Support/Quantway/Diagnostics/connection.jsonl`. Requests, cookies, device tokens, private keys and raw configurations are never logged. This diagnostic instrumentation alone does not establish a working system VPN release.

## Verification

- Shared account/subscription/desktop helper protocol tests.
- Stable native tinc identity and refusal to silently rotate an orphaned identity.
- Actual packaged tinc daemon startup with a packet FD and disabled script hooks.
- IPv4/IPv6 packet-boundary and utun-header translation.
- Assigned socket ownership, Mux and forwarding observation tests.
- Actual Darwin Xray TUN inbound driven by an in-process TCP/IP stack; traffic reaches the owned assigned outbound. This test creates no host VPN or system routes.
- Kernel-authenticated Unix peer identity.
- Regression tests: preparing/cancelling a pending tunnel changes no global routes/DNS/PF; activation orders the guard/routes before DNS; a partial route failure removes only owned state.
- Four existing Swift parser tests.
- Offline UI smoke scenarios for login, home, route checks/search, start/stop, Tinc country catalog, cabinet and external subscriptions.
- Ad-hoc code signature verification and a relocatable `.app` resource check.

Cross-compilation checks for Linux and Windows do not constitute native runtime tests on those platforms.

## Live results and remaining failure

Five diagnostic starts were explicitly authorized: one each for 0.2.3 and 0.2.4, then three iterative attempts. All five were used. A subsequent separate one-attempt, up-to-60-second allowance for 0.2.8 was also used. A later explicit allowance authorizes ten more diagnostic starts, each up to 60 seconds. Two starts from that allowance have been used (one actual network attempt and one cancelled while awaiting administrator authorization); eight remain.

- **0.2.3:** system HTTPS timed out. Cleanup restored the physical default route and removed the helpers. PF had implicitly added `flags S/SA`; already established Tinc/TLS transports were blocked at activation. Physical TCP pass counters were zero and the final drop rule recorded 226 packets.
- **0.2.4:** explicit `flags any keep state` removed that restriction. More inner TLS traffic passed, but physical transport counters remained zero and system HTTPS still timed out.
- **0.2.5, first of three:** added static interface-scoped host routes for the pinned API and ingress addresses. System HTTPS and UDP DNS passed. The verifier then rejected a valid supplemental default DNS resolver because it unnecessarily required `if_index`. Only session-owned routes are removed; matching pre-existing static routes are reused.
- **0.2.6, second of three:** exact default nameserver validation replaced the interface-annotation requirement. The correct system route and physical transport traffic were observed (434 packets / 111,222 bytes), but the system HTTPS request reached its 9-second deadline.
- **0.2.7, third of three:** bounded HTTPS retry/address rotation enabled the complete initial system route, HTTPS, UDP DNS and resolver checks to pass. Status was connected from approximately 14:20:50 UTC until the next health check degraded at approximately 14:21:12 UTC. The user reported Internet loss and confirmed manually pressing Disconnect. The launcher incorrectly reported `signal: killed` for that cancellation. The journal alone does not establish the cause of the post-start loss. System-interface byte counters are not proof of application delivery.

After the final stop, read-only checks confirmed the route to 1.1.1.1 via the physical `en0` gateway, removal of Quantway's temporary DNS, no remaining network helper/tinc daemon, and an ordinary unprivileged HTTPS request returning 200. A pre-existing supplemental resolver on another VPN interface remained unchanged.

## 0.2.8 changes and isolated capacity test

The earlier Linux failure was exhaustion of 64 physical assigned TLS connections by retained UDP sources. macOS already uses the same Mux concurrency of 8 and UDP lifecycle cleanup (10 seconds idle for DNS, 60 seconds for other UDP), with zero XUDP GlobalID.

A new macOS test ran the real Darwin Xray TUN, UDP lifecycle, Mux and VLESS/TLS path against a loopback test peer. A packet socket and in-process network stack stand in for the host kernel; only the physical overlay is substituted. It sends 1024 distinct UDP sources to port 53 at 20 per second, enforces a peer ceiling of 64 TLS connections, and checks TCP afterward. **Result: 1024/1024 replies, subsequent TCP passed, peak 26/64 physical TLS connections, 824 reclaimed logical substreams, all observed XUDP IDs zero.** The 51.16-second test changes no host interfaces/routes/DNS and contacts no production endpoint. It demonstrates that the prior accumulation defect is not reproduced in this isolated scenario; it does not disprove exhaustion on the user's live path or validate real DNS resolution/production transport.

Version 0.2.8:

- Logs active/peak/opened assigned TLS counts and last decrypted reply time. These diagnostics contain no endpoint addresses, credentials, assigned authentication UUIDs or keys.
- Sends Stop over the authenticated channel and retains the response half for the cleanup result. The launcher has a bounded 15-second cleanup grace period. A normal manual cancellation is not reported as `signal: killed`.
- After initial activation, each readiness cycle checks the actual system HTTPS/DNS path instead of indefinitely repeating only SOCKS readiness. A confirmed system-check failure ends the session and executes owned cleanup. Checks remain bounded by the current grant deadline; grant expiry/revocation retains the existing fail-closed policy until explicit stop.
- Runs the existing UDP lifecycle regression tests on macOS and Windows as well as Linux.

## Live result in 0.2.8

The additional explicitly authorized 60-second attempt ran on 2026-11-03, starting around 14:42:51 UTC (21:42:51 Bangkok). It passed the initial system route/HTTPS/UDP DNS/default resolver checks, then automatically stopped when repeated HTTPS failed with `net/http: TLS handshake timeout`. The final stop was observed within 51 seconds of the Connect click.

The current assignment opened **9 physical TLS connections**, with a peak and active count of 9 before cleanup; the last decrypted reply was at 14:43:16.531 UTC. This attempt does not reproduce accumulation of 64 local physical connections. All 11 ingress observations remained connected. The selected default DNS remained 1.1.1.1 on the Quantway supplemental resolver. Captured PF counters showed 663 physical TCP packets / 256,821 bytes and 796 inner TLS packets / 213,773 bytes. These counters do not establish successful application delivery after the last decrypted reply.

Automatic cleanup was confirmed: the physical route returned, Quantway's DNS was removed, no helper/tinc processes remained, ordinary unprivileged HTTPS returned 200, and the UI showed Disconnected with the specific system HTTPS failure. The pre-existing VPN resolver was preserved. Independent application HTTPS/DNS checks during the connected interval were not completed, so sustained client acceptance remains failed.

## 0.2.9 correction for midstream PF state

The installed macOS primary reference `/usr/share/man/man5/pf.conf.5` (TCP flags section, around lines 1680–1697) explicitly states that `flags any` can reconstruct a connection from intermediate packets without knowing TCP window scaling, and such connections can stall. This applies to Quantway's activation order: authenticated outer Tinc and inner TLS already exist when the egress guard is enabled. `flags any keep state` fixed the immediate SYN-only failure but still reconstructed these streams midway. The live pattern is consistent with this mechanism; the cause has not yet been confirmed by a successful before/after system test.

The 0.2.9 owned egress guard now uses **`flags any no state`** for its TCP-capable pass rules and `no state` for UDP. Each packet must still match the same interface/UID/port constraints and final outbound block; it no longer relies on midstream TCP sequence/window reconstruction. TLS authentication, transport ownership and route constraints are unchanged. No global PF state is flushed or modified, and other applications' rules are not replaced. Public PF failure counters are also included in subsequent error diagnostics.

The actual installed `pfctl -nf` parser accepted the candidate rules without loading them or altering the system firewall. Regression tests retain the final outbound block and require the owned pass rules to avoid state reconstruction. At the 0.2.9 stage the release status was **built_runtime_instability_unresolved**, pending its subsequent live test. The 0.2.8 automatic failure cleanup passed its live check; the 0.2.9 data-path correction has not yet been live-tested.

## Subsequent allowance: ten diagnostic starts

The user explicitly authorized ten more attempts, retaining the 60-second limit per attempt.

1. **0.2.9:** initial system checks passed, then repeated HTTPS failed with a TLS handshake timeout and automatically cleaned up. Peak assigned TLS count was only 3. A separate observer running as the ordinary user (UID 501) confirmed explicit UDP DNS failure, system resolver timeout and pinned-IP HTTPS timeout while both route snapshots still pointed at the system utun. Requests which completed after the route had restored to en0 were excluded from the tunnel result. All 11 ingress metadata connections remained observed. The no-state PF rules were confirmed installed with zero owned states, so this correction did not resolve the live failure. The owned route was restored and ordinary HTTPS returned 200. Observer results: `/private/tmp/quantway-system-check-029.jsonl`.
2. **0.2.10:** cancelled before the minute elapsed because macOS administrator authorization was still pending. The root helper never started and system routing was not switched. Cancellation completed normally without a false `signal: killed` failure. This start is conservatively counted against the allowance.

Version 0.2.10 adds kernel TCP connection information (window sizes, send buffer, MSS, bytes/retransmissions and bound interface), scoped/unscoped assigned-route snapshots, TCP queue lines restricted to the helper/native PIDs, and packet-adapter error diagnostics. It has passed local regression checks and has not yet completed a real network attempt. Further automatic starts await the user being ready to confirm the protected macOS authorization dialog. Eight authorized starts remain; no renewed network consent is needed within this allowance.

## Packet bridge failure identified in 0.2.10

The third start in the ten-start allowance completed root authorization and captured the exact failure: `write_packet_fd: write tinc-bridge: no buffer space available` (Darwin `ENOBUFS`). The original adapter returned from its forwarding goroutine on this transient packet-queue condition, permanently stopping the outbound half while native metadata links stayed alive. Kernel information showed inner TCP retransmissions with nonzero remote windows; the scoped and unscoped assigned routes both correctly selected the overlay. Outer native TCP send queues were empty. This is direct evidence of an adapter defect, unlike the earlier PF hypothesis.

Version 0.2.11 briefly retries transient ENOBUFS/EAGAIN/EINTR with bounded backpressure. If a packet queue remains full for 50 ms, it drops that IP packet and continues the adapter rather than ending the session; TCP can retransmit. Permanent descriptor/write errors remain errors. Both local datagram receive queues are sized at 256 KiB, accepted by the installed kernel.

Two new tests reproduce **actual Darwin ENOBUFS** using small Unix datagram queues. One confirms exact packet delivery after a short queue stall; the other holds congestion until one packet is dropped and then verifies that the same adapter still forwards the next packet. Both passed, as did packet boundary/address-family and other macOS lifecycle regressions. This fix has not yet completed live acceptance at this point. The user is ready to confirm system authorization; seven of the ten starts remain.

## Final live acceptance: 0.2.11

Starts 4 and 5 of the ten-start allowance used the same 0.2.11 binary. Both passed the initial actual system route/HTTPS/UDP DNS/resolver checks and continued to forward traffic. An independent ordinary-user process (UID 501) made fresh HTTPS requests to example.com, HTTPS to the pre-resolved Quantway health service with normal certificate validation, explicit UDP DNS to 1.1.1.1, and system resolver queries. Each point recorded the utun route before and after the requests; results crossing back to the physical route were excluded.

- **Start 4:** about 55 seconds connected, 3/3 independent points passed all four checks. Final system-interface totals: 7,945,104 received / 3,079,712 sent bytes; peak assigned TLS connections 11. Stopped normally and restored physical routing/DNS.
- **Start 5:** 8/8 independent points passed all four checks during the observer's first 100 seconds. The controller session unexpectedly reset and the agent failed to stop within the promised minute. It reattached and immediately stopped the session at approximately 5 minutes 28 seconds connected. This timing overrun was disclosed to the user; it was not an additional permission. Journaled forwarding/replies continued, with 89,605,366 received / 24,217,420 sent system-interface bytes and peak assigned TLS connections 16. The grant deadline advanced from 15:25:15 to 15:28:47 UTC; data continued past the original expiry. No failed status was emitted. Independent application probes were not running after the first 100 seconds, so later continuity is established by helper checks/reply observations and interface telemetry, not continuous external probe samples.

After both stops, the physical route, removal of temporary Quantway DNS, absence of helper/tinc processes and ordinary HTTPS 200 were confirmed. The UI returned to Disconnected. No further live attempts are needed for this fix. Five starts remain in the user's allowance; they were not consumed.

The source and package regression checks passed, including two tests reproducing real Darwin ENOBUFS, packet-boundary conversion, stable native identity/hooks, IPC cancellation, owned cleanup and shared client behavior. The 1024-source isolated capacity test also passed earlier (26/64 peak). Final signature/archive checks passed. Linux/Windows cross-compilation is separate from native runtime acceptance.

Final evidence summary: `docs/audits/artifacts/2026-11-03-macos-tinc-0.2.11.json`. These results confirm the repaired Tinc path in this Mac's present network. Sleep/resume, Wi-Fi changes, IPv6 Internet transport, crash recovery and other VPN modes were not acceptance-tested in this run. The existing IPv6 leak-blocking policy remains in place.

## Design handoff

See `docs/design/2026-11-03-quantway-desktop-missing-design.md`. It inventories the absent connection states and additional account/device/payment/support screens without changing the four supplied base screens.


## 0.3.0 — remaining designer screens integrated

Source: `Quantway — концепт приложения2.html`, SHA-256 `e8de95b318876444e97a59a915248b48dcf8c091d8d8b8661569d0ed3b522e4f`. Fourteen boards and 99 P1/P2 state cards are catalogued under `mobile/macos/Design`. The passive importer does not execute the attached component scripts. Demo data and timers are not wired to production connection state.

The new connection screen, Tinc countries/drawer, login/error states, sources/routes, seven cabinet tabs, device forms/QR/confirmations, payments/history/promo, referral/proxy/account flows and support composer use the supplied styles. Shared form/dialog components replace browser prompt/alert windows. Support polling preserves the draft and scroll; successful individual uploads are removed before retrying remaining attachments. The API currently accepts one image per message and has no independent subject field.

macOS adds a status-bar menu, hide-to-tray confirmation and graceful-quit confirmation. Probe progress streams from the existing CLI, with explicit cancellation. A failed native launch emits the final helper-end event so the UI cannot wait for an absent process. A distinct `network_cleanup_failed` classification preserves the cleanup failure after process end and prevents a new connection; underlying cleanup operations and the ENOBUFS forwarding fix remain intact.

Validation: four Swift tests; Go account/CLI, macOS network/IPC tests and the clienttinc bridge/readiness/mesh subset; 18 packaged-UI screenshots and ten workflow checks. Offline tests use fixtures for payments, device changes and support. Real read-only native checks confirmed launch, restored session, subscription and device data. No real payment, support send, device mutation or VPN connection was performed. The diagnostic connection allowance remains at five.

Evidence: [UI report](artifacts/2026-11-03-macos-ui-0.3.0.json), [fixture screenshots](artifacts/quantway-0.3.0-ui/), [updated designer handoff](../design/2026-11-03-quantway-desktop-missing-design.md). Crash recovery/report submission still require a system implementation; their supplied success screens are not shown without verified recovery. Ad-hoc signing and ZIP checksum are verified for the final local package.
