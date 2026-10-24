---
paths:
  - "**/*.go"
  - "**/go.mod"
---

# Go standards (enforced by Guardrails)

Guardrails formats every Go file you edit (gofumpt, goimports), lints the
package, and blocks the end of your turn until touched packages build, pass
`go test -race`, reach the coverage bar and are golangci-lint clean. Meet the
bar up front instead of on request.

## Code

- Wrap errors with context: `fmt.Errorf("load config: %w", err)`. Never drop
  an error; never `panic` for expected failures.
- `context.Context` is the first parameter of anything that does I/O or may
  block. Don't store contexts in structs.
- No package-level mutable state. Pass dependencies explicitly; accept
  interfaces, return concrete types.
- Keep exported identifiers documented with a sentence starting with the name.
- Prefer the standard library. Add a dependency only when it carries its weight.

## Tests

- Write tests in the same change as the code. Table-driven tests with
  `t.Run(tc.name, ...)` and `t.Parallel()` where safe.
- Assert behavior, not just execution: every test case checks outputs or
  errors. Coverage that only runs code proves nothing.
- Test like a mutation tester: for each condition and calculation you
  wrote, ask "if `<` became `<=`, `+` became `-`, or `&&` became `||`,
  would a test fail?" If not, add the case that would — usually the
  boundary (`n == limit`), the empty input, or the error branch.
- Use `t.TempDir()`, `t.Setenv()` and `t.Context()`; no sleeps for
  synchronization.
- Cover error paths: the coverage gate lists uncovered functions by
  file:line, and lines you changed this session must be covered too.

## Benchmarks

Add a `Benchmark*` that calls the function when it is on a hot path:

- it encodes, decodes, parses, hashes, compresses or sorts;
- it does work proportional to caller-sized input, or allocates per element;
- it's marked `// guardrails:hot` or lives in a package listed in
  `go.bench.packages`;
- you were asked to make it faster — benchmark before and after, and report
  the numbers (`-benchmem -count=6`, compared with benchstat).

Use `b.ReportAllocs()` and `b.Loop()` (or `b.N`). Don't add benchmarks to
ordinary glue code just because it loops or sorts a small slice.

## Fuzzing

Add a `Fuzz*` target that calls the function when its name says it parses,
decodes, unmarshals, scans or tokenizes, and it takes `[]byte`, a `string`
of content, or an `io.Reader` — especially if it can return an error or a
round-trip holds (`Decode(Encode(x)) == x`).

- Seed the corpus with `f.Add` using realistic and edge-case inputs.
- Assert invariants (no panic, round-trips, outputs within bounds), not just
  "it didn't crash".
- Run it briefly before finishing: `go test -run '^$' -fuzz '^FuzzX$'
  -fuzztime 10s ./pkg`. If it finds a failing input, keep the file under
  `testdata/fuzz/` — it's now a regression test.

The human can opt a function out with `// guardrails:nobench <reason>` or
`// guardrails:nofuzz <reason>`. Don't add those yourself; if you think one
is warranted, say so in one line.

## Before you finish

For non-trivial changes, run `guardrails harden` (or `/guardrails:harden`):
it mutation-tests the functions you changed and briefly fuzzes their
targets, then lists surviving mutants as `file:line` with the mutation.
Add assertions until the survivors are gone.

## Never

- `//nolint`, `t.Skip` to get green, editing `go.sum` by hand, editing
  generated files, or touching `.guardrails.yaml` / `.golangci.yml`. These are
  denied; propose changes to the human instead.
