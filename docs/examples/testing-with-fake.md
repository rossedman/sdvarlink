---
description: Unit-test application code against a varlinktest fake io.systemd.Resolve, no systemd required.
---

# Testing with a fake

:material-github: [Source](https://github.com/rossedman/sdvarlink/tree/main/examples/testing-with-fake) · :material-api: [`io.systemd.Resolve`](../reference/interfaces/resolve.md) · :material-shield-account-outline: None (runs anywhere)

Application code and its tests. `lookup.go` is a small piece of real code (an
IPv4 resolver built on `resolve.Client`); `lookup_test.go` tests it against a
`varlinktest` fake with canned answers. It runs anywhere `go test` runs, with no
root or systemd needed.

## Run it

```sh
go test -v ./examples/testing-with-fake/
```

```text title="Output (real)"
=== RUN   TestIPv4
--- PASS: TestIPv4 (0.00s)
=== RUN   TestNotFound
--- PASS: TestNotFound (0.00s)
=== RUN   TestFeatureDetection
--- PASS: TestFeatureDetection (0.00s)
PASS
ok  	github.com/rossedman/sdvarlink/examples/testing-with-fake	0.008s
```

## The code

=== "lookup.go"

    ```go title="examples/testing-with-fake/lookup.go" linenums="1"
    --8<-- "examples/testing-with-fake/lookup.go"
    ```

=== "lookup_test.go"

    ```go title="examples/testing-with-fake/lookup_test.go" linenums="1"
    --8<-- "examples/testing-with-fake/lookup_test.go"
    ```

## How it works

- **The seam.** `Resolver` holds a `*resolve.Client`. Production code builds it
  with `resolve.Dial`; the test builds it with `resolve.DialPath(ctx, srv.Path())`.
- **Typed fake handler.** `srv.Handle` takes a plain Go function from
  `ResolveHostnameIn` to `ResolveHostnameOut`. Returning
  `&resolve.NoSuchResourceRecordError{}` sends exactly the error systemd would.
- **Call assertions.** `srv.Calls()[0].Parameters` checks the JSON the code
  really sent, including that `family` was set.
- **Version-accurate introspection.** `srv.AddInterface(resolve.InterfaceDescription)`
  makes the fake answer like a v257 resolved, so `Supports("BrowseServices")`
  is `false`, just as on a real v257 host.

## See also

- [Testing with varlinktest](../guides/testing.md)
- [Record & replay](record-replay.md)
