---
description: List a Varlink service's interfaces and methods with the core package and the IDL parser.
---

# Introspect a service

:material-github: [Source](https://github.com/rossedman/sdvarlink/tree/main/examples/introspect) · :material-shield-account-outline: Depends on the socket

`varlinkctl info` plus `varlinkctl introspect`, using only the core
`varlink` package and the `idl` parser: no generated bindings. Point it at any
Varlink socket, systemd's or your own.

## Run it

```sh
go run ./examples/introspect /run/systemd/journal/io.systemd.journal
```

```text title="Output (real, systemd-journald v255)"
systemd (systemd-journald) 255 (255.4-1ubuntu8.17) (The systemd Project)

io.systemd  (0 methods, 0 types, 4 errors)

io.systemd.Journal  (4 methods, 0 types, 1 errors)
    Synchronize() -> ()
    Rotate() -> ()
    FlushToVar() -> ()
    RelinquishVar() -> ()

org.varlink.service  (2 methods, 0 types, 6 errors)
    GetInfo() -> (vendor: string, product: string, version: string, url: string, interfaces: []string)
    GetInterfaceDescription(interface: string) -> (description: string)
```

## The code

```go title="examples/introspect/main.go" linenums="1" hl_lines="23 29 36 40"
--8<-- "examples/introspect/main.go"
```

## How it works

- **`varlink.GetInfo`** calls `org.varlink.service.GetInfo`: vendor, product,
  version and the interface list.
- **`varlink.GetInterfaceDescription`** returns each interface's IDL text.
- **`idl.Parse`** turns it into an `*idl.Interface`. `idl.Type` values print
  themselves in IDL syntax, and `Method.More` reports streaming annotations.
- Both helpers take a `varlink.Caller`, so they work with a `Conn`, a `Pool`, or
  a generated client's `Caller()`.

## See also

- [Raw calls & the core package](../guides/raw-calls.md#introspection)
- [Feature detection](../guides/versioning.md#feature-detection)
