---
description: List containers and VMs registered with systemd-machined, like machinectl list.
---

# List machines

:material-github: [Source](https://github.com/rossedman/sdvarlink/tree/main/examples/machines) · :material-api: [`io.systemd.Machine`](../reference/interfaces/machine.md) · :material-tag-outline: systemd v257+ · :material-shield-account-outline: Unprivileged

`machinectl list` over Varlink: stream every container and VM registered
with `systemd-machined`. It also shows a real-world quirk: machined answers
"nothing registered" with an **error** rather than an empty stream.

## Run it

```sh
go run ./examples/machines
```

```text title="Output (illustrative)"
MACHINE  CLASS      SERVICE         UNIT                        LEADER
web      container  systemd-nspawn  systemd-nspawn@web.service  48213
win11    vm         libvirt-qemu    machine-qemu-1-win11.scope  50122

2 machines listed.
```

## The code

```go title="examples/machines/main.go" linenums="1" hl_lines="21 30 31"
--8<-- "examples/machines/main.go"
```

## How it works

- **Empty is an error.** With no machines registered, `ListMore` yields
  `io.systemd.Machine.NoSuchMachine`. `errors.Is(err, machine.ErrNoSuchMachine)`
  turns that into "zero machines".
- **Nested optional structs.** `Leader` is a `*ProcessID`; the example checks
  for `nil` before reading `PID`.
- **Pointers for optional strings**, read with `varlink.Deref`.

## See also

- [Streaming: empty results can be errors](../guides/streaming.md#supports-more-one-reply-or-many)
- [Errors](../guides/errors.md)
