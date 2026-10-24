---
name: steward
description: How to look after a pull request in sdvarlink - fixing red CI, answering review comments, finishing the IDL drift bot's systemd release PRs and getting a PR mergeable. Use when watching, babysitting or driving a PR in this repository, or when CI, a review, or a merge conflict needs handling on one.
---

# Stewarding sdvarlink pull requests

sdvarlink is a Go library with typed bindings generated from systemd's
Varlink IDL. Most of the tree is generated; the checks are strict on purpose.
These rules sit on top of the general PR-watching rules and win where they
are more specific.

## Before every push

Run `make check` (use `FUZZTIME=5s` locally). It is what CI runs, except the
integration tests: build, lint, race tests, the 90% coverage gate, doc
comments, govulncheck, bench and fuzz smoke runs, generated-code freshness
and the strict docs build. Then run `guardrails gate --base origin/main`,
which is the `gate` check.

- Install `shellcheck` locally. Without it, actionlint skips the shell in
  workflow `run:` steps and passes things the `gate` check then fails.
- New or moved test code under `examples/`, `varlink/`, `internal/`,
  `idl/` or `varlinktest/` must compile on macOS and FreeBSD as well:
  `GOOS=darwin go vet ./...` and `GOOS=freebsd go vet ./...`. Keep socket
  paths in tests short: `t.TempDir()` includes the test name and can pass
  macOS's 104-byte limit. In `varlink` tests use `sockPath(t)`.

## Never

- Edit `systemd/` or `docs/reference/` by hand. Change the IDL snapshot,
  `idl/generate.json` or the generator, then `make generate docs-reference`.
- Loosen `.guardrails.yaml` or `.golangci.yml`, add `//nolint`, or `t.Skip`
  a failing test to get green. Fix the code, or ask the maintainer.
- Rewrite history on a PR branch. PRs squash-merge, so bring `main` in with
  a merge commit.

## Reading a red check

| Check | Where the cause is |
| --- | --- |
| `gate` | The `Show the gate report` step prints the report. The guardrails action itself prints nothing on failure. |
| `fuzz (...)` | The failing input is in the `fuzz-<Target>` artifact. Decide whether the code or the test's invariant is wrong. Either way, commit the input under `<pkg>/testdata/fuzz/<Target>/` so it stays a regression test. |
| `systemd ... (fedora:...)` | The integration tests against a real systemd. A failure there is a real disagreement between the bindings and systemd. Read systemd's source for the release (`git clone --depth 1 --branch v<N> --filter=blob:none --sparse https://github.com/systemd/systemd`) before changing anything. |
| `generated code is fresh` | Run `make generate docs-reference` and commit the result. If the IDL check fails, the snapshot no longer matches `hack/extract-idl.sh`; regenerate it, don't hand-edit it. |
| `test (macOS, fake only)` | Usually a Linux-only constant or path length in a test; see "Before every push". |

A test failure that only shows up sometimes is a race until proven
otherwise: reproduce it with `go test -race -count=N -cpu=4,8`. Don't
re-run CI to make it pass.

## IDL drift PRs (`idl/systemd-v<N>`)

The weekly drift workflow opens these with the new snapshot, regenerated
bindings and an API report. Before they can merge, also:

1. **Default sockets.** For every interface new in the release, find where
   systemd serves it (`ListenStream=` in `units/*.socket`, or the daemon's
   source) and add it to `sockets` in `idl/generate.json`. Interfaces with no
   fixed socket (per-machine control sockets, ones that plugins or providers
   implement) stay out and keep `DialPath` only. Then regenerate.
2. **CHANGELOG.** Add the release under "Added", and under "Changed" list
   what breaks for `systemd/latest` users. Get that list from
   `apidiff -incompatible` between `systemd/v<old>/<pkg>` and
   `systemd/v<new>/<pkg>` (`go install golang.org/x/exp/cmd/apidiff@latest`).
3. **Docs.** Update the release list and counts in `README.md`,
   `docs/index.md` (interfaces, methods, pinned releases),
   `docs/getting-started/installation.md`, `docs/project/faq.md`,
   `docs/project/architecture.md` and `docs/project/design.md`, and add the
   new interfaces to `docs/reference/sockets.md`. Its "Since" column is
   the systemd release that introduced the interface, which can be one this
   repo has no bindings for.
4. **Integration tests.** If the release reworked an interface, add a
   read-only test in `integration/` that calls it through `systemd/latest`.
   Each test skips when the host's systemd lacks the interface or method,
   because it runs against every release in the matrix.

The workflow opens these with `GITHUB_TOKEN`, so CI doesn't start on its
own. If the PR has no checks, they start with the first push to its
branch, or when the maintainer runs them.

## Done

A PR is ready when every check on its head is green and it has no conflict.
The `main` ruleset then still requires the code owner's approval; that is
the maintainer's to give. Say once that it is waiting on review, and leave it.
