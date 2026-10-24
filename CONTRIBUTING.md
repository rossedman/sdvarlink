# Contributing to sdvarlink

Thanks for helping. This guide covers how the repository is laid out, how to
run the checks CI runs, and how to add a new systemd release.

## Quick start

```sh
git clone https://github.com/rossedman/sdvarlink && cd sdvarlink
make test        # unit tests (no systemd needed)
make race        # with the race detector
make lint        # gofmt, go vet, golangci-lint
make check       # everything CI runs
make docs-serve  # docs at http://127.0.0.1:8000 (needs uv)
```

You need Go 1.26 or newer. Linux is required for the fd-passing tests; the
core, the parser and the fake server also build and test on macOS.

## Checks

One command runs everything CI runs except the integration tests:

```sh
make check
```

It builds, lints (gofmt, go vet, golangci-lint), runs the tests with the race
detector, enforces the coverage gate, checks doc comments, runs govulncheck,
smoke-runs the benchmarks, fuzzes each fuzz target for `FUZZTIME` (30s),
checks that generated code is fresh and builds the docs site in strict mode.
Use `make check FUZZTIME=5s` for a quicker local run. You need
[golangci-lint v2](https://golangci-lint.run/welcome/install/) and
[uv](https://docs.astral.sh/uv/) (for the docs build).

## Guardrails and scanners

- [Guardrails](https://github.com/rossedman/guardrails) applies the bars
  below to every change, configured in `.guardrails.yaml`. The `Guardrails`
  workflow gates pull requests on the files they change; run the same check
  locally with `guardrails gate --base origin/main`. With Claude Code, the
  `guardrails` plugin (enabled in `.claude/settings.json`) formats and lints
  each edit and runs the gate before Claude finishes; the rules it follows are
  in `.claude/rules/`.
- [sarifix](https://github.com/rossedman/sarifix) runs golangci-lint and
  Semgrep, configured in `sarifix.yaml`. CodeQL runs in its own workflow and
  reports to code scanning; sarifix imports its alerts. On pull requests the
  `sarifix` workflow reports findings your change introduces; `sarifix check`
  does the same locally against `HEAD`. The `sarifix` Claude Code plugin runs
  it before Claude finishes. On `main`, allowed rules (`rules.allow`) become
  one GitHub issue per batch.

## Quality bars

- **Coverage:** at least 90% of hand-written code, and at least 80% per
  package (`make cover`). Generated bindings under `systemd/`, the
  generator's `main` package, `examples/` and `integration/` are excluded.
  Tests assert behavior, not just that code ran.
- **Doc comments:** every function and method, exported or not, has a doc
  comment starting with its name, and every package has a package comment;
  `make doccheck` enforces it.
- **Benchmarks:** every critical path below has one. To compare a change,
  run `make bench` on `main`, `mv bench.txt bench.old.txt`, run it again on
  your branch and `make benchstat`. Include the output in the PR.
- **Fuzzing:** every critical path that takes outside input has a fuzz
  target. If one finds a failing input, commit the file it writes under
  `testdata/fuzz/`; it becomes a regression test.

## Critical paths

These need a benchmark, and the ones that take outside input need a fuzz
target too. Update this list when you add one.

| Path | Why it matters | Benchmark | Fuzz target |
| --- | --- | --- | --- |
| `internal/wire` frame reading (`ReadMessage`) | Reads NUL-terminated frames and `SCM_RIGHTS` descriptors from any peer | `BenchmarkReadMessage` | `FuzzReadMessage` |
| `varlink.Unmarshal`, `varlink.Marshal` | Decode every reply and encode every call, including fd fields | `BenchmarkUnmarshal`, `BenchmarkMarshal` | `FuzzUnmarshal` |
| Server parameter validation (`validateParams`) | Checks untrusted client parameters against the IDL before a handler runs | `BenchmarkValidateParams` | `FuzzValidateParams` |
| `idl.Parse` | Parses IDL from snapshots and from live services' introspection | `BenchmarkParse` | `FuzzParse` |
| `Conn.Call`, `Pool`, streaming | The per-call client path | `BenchmarkCall`, `BenchmarkCallParallelPool`, `BenchmarkStream` | — |

## Layout

| Path | What lives there | Edit by hand? |
| --- | --- | --- |
| `varlink/` | Protocol core: `Conn`, `Pool`, codec, errors, introspection | yes |
| `internal/wire/` | NUL framing and `SCM_RIGHTS` on Unix sockets | yes |
| `idl/` | Varlink IDL parser (Go) and IDL snapshots (`idl/v<N>/*.varlink`) | parser yes, snapshots no |
| `idl/generate.json` | Generator config: default sockets, unstable list | yes |
| `internal/gen/`, `cmd/sdvarlink-gen/` | Code, docs and diff generator | yes |
| `systemd/v<N>/`, `systemd/latest/` | Generated bindings | **no** — `make generate` |
| `docs/reference/` | Generated interface reference | **no** — `make docs-reference` |
| `varlinktest/` | Fake server for tests | yes |
| `examples/` | Runnable examples, each a `main` package or a tested package | yes |
| `integration/` | Tests against real systemd (`-tags integration`) | yes |
| `hack/` | IDL capture (`capture-idl.sh`) and extraction (`extract-idl.sh`, `idl-from-source/`) | yes |

## Adding a systemd release

1. Extract the IDL from the release's sources (needs `git`, `cpp`, `python3`):

   ```sh
   make idl SYSTEMD_VERSION=261
   ```

   Or capture it from a booted system with `sudo hack/capture-idl.sh`. Both
   produce the same format; CI checks that the committed snapshots match
   the extractor.
2. Add default sockets for any new interfaces to `idl/generate.json`.
3. Regenerate and review the API report:

   ```sh
   make generate docs-reference
   make api-diff FROM=260 TO=261
   ```
4. Run `make check` and open a pull request with the API report in the
   description. The weekly drift workflow does steps 1–3 automatically.

Existing `systemd/v<N>/` directories change only to fix generator bugs; a
new release only ever adds a directory and moves `systemd/latest`.

## Changing the generator

Generator output is pinned by `internal/gen/testdata/demo.go.golden`. After a
deliberate change, run `go test ./internal/gen -update`, review the golden
diff, then `make generate` and review the diff under `systemd/`.

## Tests

- Unit tests must not need systemd. Use `varlinktest` for anything that
  talks to a service.
- Tests that pass file descriptors should check they do not leak (see
  `varlink/fd_test.go`).
- Integration tests call read-only methods only. Anything that changes the
  host goes behind `SDVARLINK_MUTATING=1` and runs only in disposable VMs.
- Fuzz targets: `make fuzz FUZZTIME=1m`. Add crashers to `testdata/fuzz`.

## Releases

Releases are cut with [GoReleaser](https://goreleaser.com) from
`.goreleaser.yaml`. They ship the `sdvarlink-gen` binary for Linux, macOS
and FreeBSD (amd64, arm64), bundled with the IDL snapshots; the Go packages
themselves are consumed with `go get`.

- **Every branch push** builds a snapshot, versioned like
  `0.4.1-snapshot.my-branch.a1b2c3d`. The archives and checksums are
  attached to the workflow run as the `sdvarlink-snapshot-<branch>`
  artifact (kept 14 days), with a summary on the run page.
- **Every `v*` tag** runs the tests and publishes a GitHub release with a
  grouped changelog, a `checksums.txt` signed keylessly with cosign and a
  build provenance attestation (see [SECURITY.md](SECURITY.md#verifying-releases)).
  Tags with a pre-release suffix (`v0.4.0-rc.1`) are marked as pre-releases.

```sh
make release-check   # validate .goreleaser.yaml
make snapshot        # build dist/ locally, exactly as CI does
git tag -a v0.4.0 -m "v0.4.0" && git push origin v0.4.0   # release
```

Use conventional prefixes in commit subjects (`feat:`, `fix:`, `idl:`)
so the release notes group them.

## Commits and pull requests

- Keep pull requests focused; generated changes in their own commit help
  review.
- Write commit subjects in the imperative ("varlink: close fds on decode
  error").
- Public API changes need a CHANGELOG entry and docs.

By contributing you agree that your contributions are licensed under the
MIT license, and to follow the [code of conduct](CODE_OF_CONDUCT.md).
