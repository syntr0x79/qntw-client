# Xray Darwin TUN descriptor ownership

The mobile module replaces its previous Xray dependency with the reviewed
`syntr0x79/Xray-core` fork at the full commit and pseudo-version in
`source-pin.json`. The fork starts at the exact previous upstream commit;
its production change is restricted to `proxy/tun/tun_darwin.go`.

A supplied TUN descriptor remains owned by its caller. Xray atomically creates
a CLOEXEC duplicate and closes its copy when the tunnel closes, including
failed or repeated assignment lifecycles. Supplying an interface still leaves
host routes and interface configuration under the application's control.
Explicit `Close` is idempotent. A later GC cannot close a descriptor reused by
the next assignment.

`mobile/scripts/with-xray-darwin.sh COMMAND ...` verifies the selected Go module,
complete upstream/fork archive hashes, source file, regression tests and that
the reviewed patch produces the pinned fork source. It writes source and
license receipts to a private build directory and exports their manifest and
fingerprint for the Mac build. It does not set `GOFLAGS`, rewrite `go.mod`,
or modify the shared Go module cache. Ordinary Go module resolution selects
the same fix in helper, engine and gomobile-generated modules.

```sh
mobile/scripts/with-xray-darwin.sh go -C mobile test \
  github.com/xtls/xray-core/proxy/tun -run '^TestQuantway' -count=1 -v
go -C mobile test ./clienttinc -count=1
```

The dependency regression checks caller survival after Close and GC,
synchronous duplicate closure, CLOEXEC, 32 same-process FD reuse cycles and
invalid descriptor rejection. The earlier subprocess isolation workaround for
the 1024-source client test is removed, so it runs after the other TUN tests in
the same process. No real system interface or host route is changed by these
descriptor tests.
