---
description: Call any Varlink method with JSON parameters and print the replies, like varlinkctl call.
---

# Raw calls

:material-github: [Source](https://github.com/rossedman/sdvarlink/tree/main/examples/raw-call) · :material-shield-account-outline: Depends on the socket

A 70-line `varlinkctl call`: invoke any method with JSON parameters, with
`-more` for streams and `-oneway` for fire-and-forget. It exercises every call
shape of the core package with `json.RawMessage` in and out.

## Run it

```sh
go run ./examples/raw-call /run/systemd/journal/io.systemd.journal org.varlink.service.GetInfo
go run ./examples/raw-call -more /run/systemd/userdb/io.systemd.Multiplexer \
    io.systemd.UserDatabase.GetUserRecord '{"service":"io.systemd.Multiplexer"}'
```

```text title="Output (real, systemd-journald v255)"
{
  "vendor": "The systemd Project",
  "product": "systemd (systemd-journald)",
  "version": "255 (255.4-1ubuntu8.17)",
  "url": "https://systemd.io/",
  "interfaces": [
    "io.systemd",
    "io.systemd.Journal",
    "org.varlink.service"
  ]
}
```

## The code

```go title="examples/raw-call/main.go" linenums="1" hl_lines="45 47 57 63"
--8<-- "examples/raw-call/main.go"
```

## How it works

- **`json.RawMessage` in and out.** Parameters pass through untouched, and
  replies decode into `json.RawMessage`, so the tool works with any method.
- **Three call shapes:** `Call` (one reply), `Stream` (sets `more`, yields
  `*varlink.Reply`), `Oneway` (no reply).
- **`r.Close()`** releases any file descriptors a streamed reply carried, since
  this tool never decodes them.
- **Errors** print their qualified name and raw parameters from
  `*varlink.Error`.

## See also

- [Raw calls & the core package](../guides/raw-calls.md)
- [Errors: untyped errors](../guides/errors.md#untyped-errors-varlinkerror)
