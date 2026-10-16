---
description: Resolve many names in parallel with one pooled generated client.
---

# Concurrent lookups

:material-github: [Source](https://github.com/rossedman/sdvarlink/tree/main/examples/concurrent-lookups) · :material-api: [`io.systemd.Resolve`](../reference/interfaces/resolve.md) · :material-tag-outline: systemd v257+ · :material-shield-account-outline: Unprivileged

Fan out DNS lookups across goroutines through **one** `resolve.Client`.
Generated clients keep a pool of connections, so the lookups overlap instead of
queueing behind each other on a single socket, where Varlink would answer them
one at a time.

## Run it

```sh
go run ./examples/concurrent-lookups github.com go.dev systemd.io kernel.org
```

```text title="Output (illustrative)"
github.com           1 addresses in 21ms
go.dev               8 addresses in 24ms
systemd.io           8 addresses in 19ms
kernel.org           2 addresses in 22ms
4 lookups in 25ms
```

## The code

```go title="examples/concurrent-lookups/main.go" linenums="1" hl_lines="23 35 38"
--8<-- "examples/concurrent-lookups/main.go"
```

## How it works

- **One client, many goroutines.** `resolve.Client` is safe for concurrent
  use. Each concurrent call borrows its own connection from the client's pool,
  and returns it when done.
- **The total is close to the slowest lookup**, not the sum; that's the pool
  at work. With a single `varlink.Conn`, calls would serialize.
- **One context for all.** The 15-second timeout bounds every lookup; when it
  expires, in-flight calls return `context.DeadlineExceeded`.
- For thousands of names, bound concurrency with a semaphore; see
  [Concurrency & pools](../guides/concurrency.md#bounding-concurrency).

## See also

- [Concurrency & pools](../guides/concurrency.md)
- [Resolve a hostname](resolve-hostname.md)
