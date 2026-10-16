---
description: Resolve names through systemd-resolved's io.systemd.Resolve interface, like resolvectl query.
---

# Resolve a hostname

:material-github: [Source](https://github.com/rossedman/sdvarlink/tree/main/examples/resolve-hostname) · :material-api: [`io.systemd.Resolve`](../reference/interfaces/resolve.md) · :material-tag-outline: systemd v257+ · :material-shield-account-outline: Unprivileged

A tiny `resolvectl query`: resolve one or more names through `systemd-resolved`
and print every address. It shows the everyday shape of an sdvarlink program —
dial, call, handle typed errors — and how to fall back when the service is
missing.

## Run it

```sh
go run ./examples/resolve-hostname github.com systemd.io nope.invalid
```

```text title="Output (illustrative)"
github.com               140.82.121.3
systemd.io               185.199.108.153
systemd.io               185.199.109.153
systemd.io               185.199.110.153
systemd.io               185.199.111.153
nope.invalid: no such host
```

## The code

```go title="examples/resolve-hostname/main.go" linenums="1" hl_lines="27 28 37 40"
--8<-- "examples/resolve-hostname/main.go"
```

## How it works

- **`resolve.Dial(ctx)`** connects to `resolve.DefaultSocket`. If resolved
  isn't running, or systemd predates the interface, the error matches
  `varlink.ErrUnsupported`, which the program turns into a friendly message.
- **`ResolveHostname`** takes a `ResolveHostnameIn`; only `Name` is required.
  Set `Family: varlink.Ptr(int64(syscall.AF_INET))` to ask for IPv4 only.
- **Typed errors.** A name that doesn't exist comes back as
  `*resolve.NoSuchResourceRecordError`; `errors.As` (or
  `errors.Is(err, resolve.ErrNoSuchResourceRecord)`) separates it from real
  failures.
- **Addresses** arrive as JSON arrays of byte values (`[]int64`), which
  `toAddr` converts to a `netip.Addr`.

## See also

- [Typed clients](../guides/typed-clients.md)
- [Errors](../guides/errors.md)
- [Concurrent lookups](concurrent-lookups.md)
