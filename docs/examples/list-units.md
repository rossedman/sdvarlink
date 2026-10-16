---
description: List units through PID 1's io.systemd.Unit interface (systemd v258+), with feature detection.
---

# List units

:material-github: [Source](https://github.com/rossedman/sdvarlink/tree/main/examples/list-units) · :material-api: [`io.systemd.Unit`](../reference/interfaces/unit.md) · :material-tag-outline: systemd v258+ · :material-shield-account-outline: Unprivileged

A `systemctl list-units` over Varlink, using `io.systemd.Unit.List`, which PID 1
gained in systemd v258. It checks support at run time first, which is
where a real program would fall back to `go-systemd`'s D-Bus API.

## Run it

```sh
go run ./examples/list-units
go run ./examples/list-units -unit ssh.service
```

```text title="Output (illustrative)"
UNIT                      LOAD    ACTIVE    SUB       DESCRIPTION
-.mount                   loaded  active    mounted   Root Mount
dbus.service              loaded  active    running   D-Bus System Message Bus
ssh.service               loaded  active    running   OpenBSD Secure Shell server
systemd-journald.service  loaded  active    running   Journal Service
systemd-resolved.service  loaded  active    running   Network Name Resolution
timers.target             loaded  active    active    Timer Units
```

## The code

```go title="examples/list-units/main.go" linenums="1" hl_lines="26 33 45"
--8<-- "examples/list-units/main.go"
```

## How it works

- **Feature detection.** `c.Supports(ctx, "List")` fetches the live interface
  description and checks for the method before calling it.
- **Streaming.** `List` supports `more`; `ListMore` yields one `*ListOut` per
  unit, with a `Context` (configuration) and `Runtime` (state) each.
- **Pinned to v258.** In the v258 bindings `Runtime.LoadState` and
  `ActiveState` are plain strings. In v260 (and `latest`) they became optional
  `*string`, so with `latest` you'd write `varlink.Deref(u.Runtime.ActiveState)`.
  See [Versioning](../guides/versioning.md#required-fields-become-optional).
- **`-unit`** sets the optional `Name` filter with `varlink.Ptr`.

## See also

- [Versioning & feature detection](../guides/versioning.md)
- [Migrating from D-Bus: list units](../guides/migrating-from-dbus.md#list-units)
