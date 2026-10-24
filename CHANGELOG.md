# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
uses [semantic versioning](https://semver.org/) for the core, `varlinktest`,
`idl` and the generator. Generated packages under `systemd/v<N>` only gain
new release directories; see [API changes](https://rossedman.github.io/sdvarlink/reference/api-changes/)
for what changed in systemd itself.

## [Unreleased]

### Added

- `varlink.Server`: serve Varlink interfaces from Go.
  - Typed handlers (`Handle`, `HandleStream`) checked against the IDL at
    registration, and parameters validated against it per call, as
    sd-varlink does.
  - Live and list streams (`LastIsFinal`), `AllowSingle`,
    `AllowUnknownFields`, `oneway`, pipelined calls answered in order, and
    file descriptors in both directions.
  - Built-in `org.varlink.service` introspection, standard errors, and
    handler errors mapped to `io.systemd.System`.
  - Peer credentials (`Call.Peer`), `Listen` and `ListenActivated` (systemd
    socket activation by `FileDescriptorName=`), and graceful shutdown.
  - Connection, frame, fd and idle limits.
- Examples `queued` (a complete service) and `metrics-provider` (serves
  `io.systemd.Metrics` with the generated types).
- Bindings for systemd v262 under `systemd/v262/`: 46 interfaces, 13 of
  them new (`CryptEnroll`, `InstanceMetadata`, `Job`, `MachineInstance`,
  `Report`, `Report.Signer`, `Report.Uploader`, `Shutdown`,
  `StorageProvider`, `SysInstall`, `SysUpdate`, `SysUpdate.Notify`,
  `VirtualMachineInstance`), and new methods such as `Unit.StartTransient`,
  `Login.ListSessions`, `Hostname.SetHostname` and `Manager.Reboot`. See
  [API changes](https://rossedman.github.io/sdvarlink/reference/api-changes/)
  for everything systemd changed.

### Changed

- **`systemd/latest` now re-exports v262** (was v260). systemd v262 turned
  many plain fields into enums or made them optional, so code using these
  through `latest` may no longer compile:
  - `unit`: most of `ExecContext`, `UnitContext` and `CGroupContext`
    (for example `ExecContext.NoNewPrivileges` is now `*bool` and
    `ExecContext.ProtectSystem` is now `*ProtectSystem`), and
    `WorkingDirectory.Path`.
  - `manager`: `ManagerContext.LogTarget`, `DefaultOOMPolicy`,
    `DefaultStandardOutput`, `DefaultStandardError`,
    `DefaultMemoryPressureWatch` and `CtrlAltDelBurstAction` are enums.
  - `machine`: `KillIn.Whom` is `*KillWhom`; `ListOut.Class` and
    `RegisterIn.Class` are `MachineClass`.
  - `managedoom` and `oom`: `ControlGroup.Mode` is `ManagedOOMMode`.
  - `bootcontrol`: `BootEntryAddon.Options` is `*string`.
  - `repart`: `ListCandidateDevicesOut.Node` is `*string`.

  Enums are string types, so untyped string constants still compile. To
  keep the old types, import `systemd/v260/...` instead of `latest`.
- `Conn.Stream`, and with it every generated `...More` method and
  `StreamAs`, no longer yields a final reply that has no parameters and no
  file descriptors. sd-varlink services send one to end an empty stream
  (logind's `ListSessions` with no sessions, for example), and
  `HandleStream` sends one when a live stream ends; both used to show up as
  one zero-valued item. An empty list now yields nothing.

### Fixed

- The frame size limit now also applies to complete messages that arrive in
  a single read, on both the client and server side.

## [0.1.0]

### Added

- `varlink`: Varlink client core — `Dial`, `DialService`, `NewConn`,
  `Conn.Call`, `CallReply`, `Stream` (as `iter.Seq2`), `Oneway`; `Pool`;
  `StreamAs`; context deadlines and cancellation.
- File-descriptor passing in both directions with `SCM_RIGHTS`, via
  `*os.File` fields tagged `varlink:"name,fd"`. Unclaimed descriptors are
  closed; received descriptors are close-on-exec.
- Errors: `*varlink.Error` matched by name with `errors.Is`,
  `*varlink.SystemError` unwrapping to `syscall.Errno`, sentinels for
  `org.varlink.service` errors, `ErrUnsupported` for missing services.
- Introspection: `GetInfo`, `GetInterfaceDescription`, `Supports`.
- `idl`: parser for the Varlink IDL including systemd's streaming
  annotations.
- `sdvarlink-gen`: generates typed packages, the interface reference and
  release-to-release API reports.
- Generated bindings for every `io.systemd.*` interface in systemd v257,
  v258 and v260, plus `systemd/latest`.
- `varlinktest`: fake server with typed (`Handle`, `On`, `OnStream`) and raw
  handlers, introspection, call recording, and record/replay golden files.
- IDL extraction from systemd sources (`hack/extract-idl.sh`) and capture
  from a running system (`hack/capture-idl.sh`).
- 15 runnable examples and a documentation site.
- GoReleaser releases of `sdvarlink-gen` for Linux, macOS and FreeBSD, with
  a snapshot build on every branch push.

[Unreleased]: https://github.com/rossedman/sdvarlink/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/rossedman/sdvarlink/releases/tag/v0.1.0
