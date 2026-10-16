---
description: Read the io.systemd.Metrics interface systemd services publish under /run/systemd/report/ (v260+).
---

# Read metrics

:material-github: [Source](https://github.com/rossedman/sdvarlink/tree/main/examples/metrics) · :material-api: [`io.systemd.Metrics`](../reference/interfaces/metrics.md) · :material-tag-outline: systemd v260+ · :material-shield-account-outline: Unprivileged

systemd v260 introduced `io.systemd.Metrics`: services publish metric
families on sockets under `/run/systemd/report/`. This example discovers those
sockets and streams every metric from each, which is the building block for
exporters such as report2otel.

## Run it

```sh
go run ./examples/metrics
go run ./examples/metrics /run/systemd/report/io.systemd.Network
```

```text title="Output (illustrative)"
# io.systemd.Manager
io.systemd.Manager.unitsByTypeTotal 12
io.systemd.Manager.unitsByTypeTotal 41
io.systemd.Manager.unitActiveState{dev-hvc0.device} "active"
io.systemd.Manager.unitActiveState{ssh.service} "active"
```

## The code

```go title="examples/metrics/main.go" linenums="1" hl_lines="30 37 42"
--8<-- "examples/metrics/main.go"
```

## How it works

- **No default socket.** Many daemons implement `io.systemd.Metrics`, each
  on its own socket, so the package has `DialPath` but no `Dial`.
- **Requires-more.** `List` streams one reply per metric; it takes no
  parameters, so the method is just `List(ctx)`.
- **Untyped values.** `Value` is IDL type `any`, so it's a `json.RawMessage`;
  `Fields` (`?object`) distinguishes metrics within a family (e.g. by unit
  type). Use `Describe` to learn each family's type (`counter`, `gauge`,
  `string`).

## See also

- [Streaming](../guides/streaming.md)
- [Publish metrics](metrics-provider.md): the server side
