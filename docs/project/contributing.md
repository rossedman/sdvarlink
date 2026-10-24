---
description: How to build, test and contribute to sdvarlink, including adding a new systemd release.
---

# Contributing

Thanks for helping! This page mirrors
[`CONTRIBUTING.md`](https://github.com/rossedman/sdvarlink/blob/main/CONTRIBUTING.md),
which is canonical.

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
  Semgrep, configured in `sarifix.yaml`. CodeQL runs as GitHub's code scanning
  default setup; sarifix imports its alerts. On pull requests the
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

## Make targets

| Target | What it does |
| --- | --- |
| `make test` | Run unit tests. |
| `make race` | Run unit tests with the race detector. |
| `make check` | Everything CI runs except integration tests; `make check FUZZTIME=5s` for a quick run. |
| `make cover` | Fail if hand-written code is below 90% coverage; writes `coverage.html`. |
| `make lint` | `gofmt` check, `go vet`, `golangci-lint`. |
| `make doccheck` | Fail if a function, method or package has no doc comment. |
| `make vuln` | `govulncheck` over every package. |
| `make fuzz` | Fuzz the frame reader, codec, parameter validation and IDL parser for `FUZZTIME` each (default 30s): `make fuzz FUZZTIME=1m`. |
| `make bench` | Benchmarks of the critical paths, saved to `bench.txt`. |
| `make bench-smoke` / `make benchstat` | Run every benchmark once / compare `bench.old.txt` with `bench.txt`. |
| `make integration` | Integration tests against the host's systemd (`-tags integration`). Read-only unless `SDVARLINK_MUTATING=1`. |
| `make generate` | Regenerate `systemd/` packages from `idl/` snapshots. |
| `make docs-reference` | Regenerate the interface reference pages and the `mkdocs.yml` nav block. |
| `make check-generated` | Fail if generated code or docs are stale (CI runs this). |
| `make idl SYSTEMD_VERSION=261` | Extract IDL for a release from systemd sources (needs `git`, `cpp`, `python3`). |
| `make idl-capture` | Capture IDL from the running systemd (needs root). |
| `make api-diff FROM=257 TO=260` | Print API changes between two releases. |
| `make docs-serve` | Serve the docs locally (regenerates the reference first; needs uv). |
| `make docs` | Build the docs into `site/` with `--strict`. |
| `make release-check` / `make snapshot` | Validate the GoReleaser config / build a local snapshot into `dist/`. |

## Repository layout

| Path | What lives there | Edit by hand? |
| --- | --- | --- |
| `varlink/` | Protocol core: `Conn`, `Pool`, codec, errors, introspection | yes |
| `internal/wire/` | NUL framing and `SCM_RIGHTS` on Unix sockets | yes |
| `idl/` | IDL parser (Go) and snapshots (`idl/v<N>/*.varlink`) | parser yes, snapshots no |
| `idl/generate.json` | Generator config: default sockets, unstable list | yes |
| `internal/gen/`, `cmd/sdvarlink-gen/` | Code, docs and diff generator | yes |
| `systemd/v<N>/`, `systemd/latest/` | Generated bindings | **no**: `make generate` |
| `docs/reference/interfaces*`, `docs/reference/api-changes.md` | Generated reference | **no**: `make docs-reference` |
| `docs/` (everything else), `mkdocs.yml` | This site | yes |
| `varlinktest/` | Fake server for tests | yes |
| `examples/` | Runnable examples (embedded in these docs) | yes |
| `integration/` | Tests against real systemd | yes |
| `hack/` | IDL capture and extraction | yes |

## Adding a systemd release

1. Extract the IDL from the release's sources:

    ```sh
    make idl SYSTEMD_VERSION=261
    ```

    Or capture it from a booted system with `sudo hack/capture-idl.sh`. Both
    produce the same format.

2. Add default sockets for any new interfaces to `idl/generate.json`.
3. Regenerate and review the API report:

    ```sh
    make generate docs-reference
    make api-diff FROM=260 TO=261
    ```

4. Run `make check` and open a pull request with the API report in the
   description.

The weekly drift workflow does steps 1–3 automatically. Existing
`systemd/v<N>/` directories change only to fix generator bugs; a new release
only adds a directory and moves `systemd/latest`.

## Changing the generator

Generator output is pinned by `internal/gen/testdata/demo.go.golden`. After a
deliberate change, run `go test ./internal/gen -update`, review the golden diff,
then `make generate` and review the diff under `systemd/`.

## Tests

- Unit tests must not need systemd. Use [`varlinktest`](../guides/testing.md)
  for anything that talks to a service.
- Tests that pass file descriptors should check that they don't leak (see
  `varlink/fd_test.go`).
- Integration tests call read-only methods only; anything that changes the
  host goes behind `SDVARLINK_MUTATING=1` and runs only in disposable VMs.
- Add fuzz crashers to `testdata/fuzz`.

## Working on the docs

```sh
make docs-serve   # live reload (needs uv) at http://127.0.0.1:8000
make docs         # what CI runs: mkdocs build --strict
```

- Pages are Markdown under `docs/`, rendered with
  [Material for MkDocs](https://squidfunk.github.io/mkdocs-material/).
- Example programs are embedded straight from `examples/` with snippets
  (`--8<-- "examples/<name>/main.go"`), so they can't drift from the code.
- Every Go block should compile against the current API.
- Diagrams are [Mermaid](https://mermaid.js.org/) fences.

## Commits and pull requests

- Keep pull requests focused; put generated changes in their own commit.
- Write commit subjects in the imperative, with a conventional prefix so the
  release notes group them (`feat:`, `fix:`, `idl:`), e.g.
  `fix: varlink: close fds on decode error`.
- Public API changes need a CHANGELOG entry and docs.

By contributing you agree that your contributions are licensed under the MIT
license, and to follow the
[code of conduct](https://github.com/rossedman/sdvarlink/blob/main/CODE_OF_CONDUCT.md).
