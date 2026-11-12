# Quantway native clients

Android (including TV), macOS, Linux and Windows clients with a shared Go VPN
engine. This repository is independent of `syntr0x79/qntw-backend`: no backend
checkout, database, production credentials or deployment runner is required.

## Layout

- `mobile/android` — Kotlin/Compose app and Android TV screens.
- `mobile/macos` — Swift desktop app and privileged networking helper.
- `mobile/linux` — GTK desktop app and isolated network helpers.
- `mobile/windows` — desktop app, helpers and installer.
- `mobile/engine`, `mobile/clienttinc`, `mobile/tincbridge` — common client runtime.
- `internal` — client-side share-link parsers, transport probes and wire formats.
  This is a small client-owned subset; server repositories, billing, persistence,
  admin APIs and deployment code stay in the backend repository.
- `mobile/native`, `deploy/*/patches` — pinned upstream sources and reviewed native
  patches used by the client builders; not infrastructure deployment scripts.

The root Go module provides internal client libraries. `mobile/go.mod` is a
second module whose only local replacement points to this same repository's
root (`..`), not to a sibling backend checkout. Xray and tinc retain their
existing pinned forks; their provenance checks and third-party notices remain.

## Checks

Use Go 1.27, Python 3 and `make`:

```sh
make test
make vet
make test-python
```

Platform networking tests requiring TUN, privileges or an installed native tinc
bundle are explicit integration tests and may skip in ordinary unit runs.
Source-overlay regression tests under `mobile/native/xray/tests` run inside the
reviewed Xray fork, not as an independent application package.

## Build

Android requires JDK 17, Android SDK 35 / Build Tools 35.0.0 and NDK
28.0.13004108. Export `JAVA_HOME` and `ANDROID_HOME`, then `make mobile-apk`.
GitHub Actions builds the engine and a debug APK without release signing keys.

Desktop builders and prerequisites are documented in:

- [Linux](mobile/linux/README.md) — `make linux`.
- [macOS](mobile/macos/README.md) — `make macos` on Apple Silicon with Xcode.
- [Windows](mobile/windows/README.md) — `make windows` with Docker/native tools.
- [Android and shared engine](mobile/README.md).

Unit checks and Linux/Windows cross-compilation have their own Actions workflow.
Desktop packaging requires the documented toolchains; a compile check does not
claim signed release or full system-VPN acceptance. Public APIs at `qntw.link`
remain in the backend repository and their wire contracts are unchanged.

## Migration

Imported from `qntw-backend` on 2026-11-12. Android commit history is preserved;
current uncommitted desktop and transport source files are included in the
migration commit. `MIGRATION.json` records the source revision and shared-source
checksums. Existing backend integration harnesses consume client artifacts via
explicit paths. Do not copy account data, subscription tokens, local SDK settings,
signing keys or compiled release artifacts into Git.
