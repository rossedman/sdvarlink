---
description: Install sdvarlink and check that your Go toolchain, systemd and platform are supported.
---

# Installation

sdvarlink is a single Go module. Add it to your project:

```sh
go get github.com/rossedman/sdvarlink@latest
```

That's all. There is no cgo, no code generation step on your side and no
daemon to run: the generated packages are plain Go, checked in, and the
runtime depends only on the standard library and `golang.org/x/sys`.

## Requirements

| Component | Supported | Notes |
| --- | --- | --- |
| **Go** | 1.26 or newer | Streaming uses range-over-func iterators (`iter.Seq2`). The module tracks the two newest Go releases. |
| **systemd** | v257 or newer | The floor. Bindings are generated for v257, v258 and v260; individual interfaces appeared later (see [Default sockets](../reference/sockets.md)). |
| **OS** | Linux | Other platforms compile the core and `varlinktest` (so tests run on a Mac), but `Dial` returns `varlink.ErrUnsupported`. |
| **Architecture** | amd64, arm64 | Tested. Other Linux architectures are expected to work; nothing is architecture-specific. |
| **Dependencies** | stdlib + `golang.org/x/sys` | The generator may use more; the runtime never does. |
| **cgo** | not used | Static binaries, `CGO_ENABLED=0` friendly. |

!!! tip "Older systemd? Keep going."

    On a host with systemd older than v257, or where a service isn't running,
    `Dial` fails with an error that matches `varlink.ErrUnsupported`. That's
    your cue to fall back to D-Bus with `go-systemd`; see
    [Versioning & feature detection](../guides/versioning.md#fallback-to-d-bus).

## Check your host

Find your systemd version and the Varlink sockets it serves:

```sh
systemctl --version | head -1
ls /run/systemd/io.systemd.* /run/systemd/*/io.systemd.* 2>/dev/null
```

```text title="Example output on a systemd v258 host"
systemd 258 (258.1-1)
/run/systemd/io.systemd.BootControl   /run/systemd/io.systemd.Manager
/run/systemd/io.systemd.Credentials   /run/systemd/io.systemd.MountFileSystem
/run/systemd/io.systemd.Hostname      /run/systemd/io.systemd.NamespaceResource
/run/systemd/io.systemd.Login         /run/systemd/io.systemd.PCRExtend
/run/systemd/journal/io.systemd.journal
/run/systemd/machine/io.systemd.Machine
/run/systemd/resolve/io.systemd.Resolve
/run/systemd/userdb/io.systemd.Multiplexer
```

`varlinkctl`, which ships with systemd, is the quickest way to poke at a
service before writing Go:

```sh
varlinkctl info /run/systemd/io.systemd.Hostname
varlinkctl call /run/systemd/io.systemd.Hostname io.systemd.Hostname.Describe '{}'
```

## Choose your imports

You will mostly import generated packages, plus the core when you need
helpers such as `varlink.Ptr` or the error sentinels:

```go
import (
    "github.com/rossedman/sdvarlink/systemd/v257/resolve" // pinned to the v257 API
    "github.com/rossedman/sdvarlink/varlink"              // core: Dial, Pool, errors, Ptr/Deref
)
```

| Import path | Use it for |
| --- | --- |
| `github.com/rossedman/sdvarlink/systemd/v<N>/<pkg>` | A typed client pinned to systemd release *N*. Pick the oldest release you support. |
| `github.com/rossedman/sdvarlink/systemd/latest/<pkg>` | Aliases of the newest generated release (currently v260). |
| `github.com/rossedman/sdvarlink/varlink` | The protocol core: connections, pools, raw calls, errors, introspection. |
| `github.com/rossedman/sdvarlink/varlinktest` | The fake server for unit tests. |
| `github.com/rossedman/sdvarlink/idl` | The Varlink IDL parser. |

See [Versioning & feature detection](../guides/versioning.md) to decide
between a pinned and the `latest` package.

## Permissions

sdvarlink adds no privileges. Whatever `varlinkctl` can do as your user, your
Go program can do, and no more: systemd checks the caller's credentials
(`SO_PEERCRED`) and polkit on every call. Read-only methods such as
`io.systemd.Resolve.ResolveHostname` or `io.systemd.Hostname.Describe` work
unprivileged; methods like `io.systemd.Journal.Rotate` or
`io.systemd.MountFileSystem.MountImage` need root or a polkit grant. More in
the [Security model](../guides/security.md).

<div class="grid cards" markdown>

-   :material-play-circle-outline: **Next: write your first program**

    ---

    [:octicons-arrow-right-24: Quickstart](quickstart.md)

</div>
