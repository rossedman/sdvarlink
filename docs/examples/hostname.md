---
description: Print what systemd-hostnamed knows about the machine, like hostnamectl.
---

# Describe the host

:material-github: [Source](https://github.com/rossedman/sdvarlink/tree/main/examples/hostname) · :material-api: [`io.systemd.Hostname`](../reference/interfaces/hostname.md) · :material-tag-outline: systemd v257+ · :material-shield-account-outline: Unprivileged

`hostnamectl` in 40 lines. One `Describe` call returns everything
`systemd-hostnamed` knows about the machine: hostnames, chassis, OS, kernel,
hardware and firmware. Optional fields are pointers; `varlink.Deref` reads them
without nil checks.

## Run it

```sh
go run ./examples/hostname
```

```text title="Output (illustrative)"
   Static hostname: build-07
Transient hostname: build-07
         Icon name: computer-server
           Chassis: server
  Operating system: Fedora Linux 42 (Server Edition)
            Kernel: Linux 6.14.4-300.fc42.x86_64
   Hardware vendor: Dell Inc.
    Hardware model: PowerEdge R650
```

## The code

```go title="examples/hostname/main.go" linenums="1" hl_lines="16 22 31"
--8<-- "examples/hostname/main.go"
```

## How it works

- **`Describe`** takes a `DescribeIn` whose only field,
  `AllowInteractiveAuthentication`, is optional, so an empty struct is fine.
- **Required vs optional.** `Hostname`, `KernelName` and `KernelRelease` are
  plain strings (always present); `StaticHostname`, `Chassis` and friends are
  `*string` because a host may not set them.
- **`varlink.Deref`** returns `""` for unset fields, and `row` skips empty
  values, which mirrors `hostnamectl`'s output.
- The example pins `systemd/v257/hostname`. See
  [Versioning](../guides/versioning.md#when-a-field-changes-type) for a field
  in this interface whose type changed between releases.

## See also

- [Typed clients: optional fields](../guides/typed-clients.md#optional-fields-ptr-and-deref)
- [Migrating from D-Bus](../guides/migrating-from-dbus.md)
