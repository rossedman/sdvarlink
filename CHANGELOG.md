# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and the project
uses [semantic versioning](https://semver.org/) for the core, `varlinktest`,
`idl` and the generator. Generated packages under `systemd/v<N>` only gain
new release directories; see [API changes](https://rossedman.github.io/sdvarlink/reference/api-changes/)
for what changed in systemd itself.

## [Unreleased]

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

[Unreleased]: https://github.com/rossedman/sdvarlink/commits/main
