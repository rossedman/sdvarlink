---
description: Publish metrics over io.systemd.Metrics using the generated v260 types, readable by systemd tooling and examples/metrics.
---

# Publish metrics

:material-github: [Source](https://github.com/rossedman/sdvarlink/tree/main/examples/metrics-provider) · :material-api: [`io.systemd.Metrics`](../reference/interfaces/metrics.md) · :material-tag-outline: systemd v260 interface · :material-shield-account-outline: Unprivileged

systemd v260 services publish metrics by serving `io.systemd.Metrics` on a
socket under `/run/systemd/report/`. This example does the same from Go. It
serves the systemd interface with the **generated** types: the handlers take
`metrics.ListOut` and `metrics.DescribeOut`, and the IDL is
`metrics.InterfaceDescription`. It is the server-side counterpart of
[Read metrics](metrics.md).

## Run it

```sh
go run ./examples/metrics-provider -socket /tmp/demo.metrics
```

```text title="Output (captured)"
$ go run ./examples/metrics /tmp/demo.metrics
# demo.metrics
io.example.MetricsDemo.goroutines 6
io.example.MetricsDemo.uptimeSeconds 0
io.example.MetricsDemo.listCalls 1
io.example.MetricsDemo.build{go} "go1.24.7"

$ varlinkctl call --more /tmp/demo.metrics io.systemd.Metrics.Describe '{}'
{"name":"io.example.MetricsDemo.goroutines","description":"Goroutines in the provider process","type":"gauge"}
{"name":"io.example.MetricsDemo.uptimeSeconds","description":"Seconds since the provider started","type":"counter"}
{"name":"io.example.MetricsDemo.listCalls","description":"List calls served","type":"counter"}
{"name":"io.example.MetricsDemo.build","description":"Build information","type":"string"}
```

## The code

```go title="examples/metrics-provider/main.go" linenums="1"
--8<-- "examples/metrics-provider/main.go"
```

## How it works

- **Generated types on the server side.** Registering
  `metrics.InterfaceDescription` and handling with `metrics.ListOut` means the
  types are checked against systemd's own IDL at startup.
- **`LastIsFinal()`.** `List` and `Describe` return finite lists, so the last
  metric is the final reply and clients don't see a trailing empty item. This
  is the same thing systemd's own providers do.
- **Requires `more`.** The IDL marks both methods `# [Requires 'more' flag]`,
  so a plain call gets `org.varlink.service.ExpectedMore`.
- **Socket placement.** To be found by tools that scan `/run/systemd/report/`,
  install a `.socket` unit with `ListenStream=/run/systemd/report/<name>`. The
  provider picks it up through `ListenActivated`.

## See also

- [Serving Varlink](../guides/server.md)
- [Read metrics](metrics.md)
