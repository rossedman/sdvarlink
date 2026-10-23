---
description: A small job-queue service served over Varlink with typed handlers, a stream, peer-credential checks and socket activation.
---

# A job queue service

:material-github: [Source](https://github.com/rossedman/sdvarlink/tree/main/examples/queued) · :material-api: `io.example.Queue` (its own interface) · :material-shield-account-outline: Unprivileged

`queued` is a complete Varlink service in about 200 lines. It serves its own
interface, `io.example.Queue`, and shows every part of the server:

- a typed call (`Depth`);
- a mutating call that checks the caller's UID (`Enqueue`);
- a live stream (`Watch`);
- a typed error (`QueueFull`);
- validation against the IDL;
- socket activation with a fallback to binding the path itself;
- clean shutdown on `SIGTERM`.

## Run it

```sh
go run ./examples/queued -socket /tmp/queued.sock
```

Then talk to it with systemd's `varlinkctl`, or with sdvarlink's client:

```text title="Output (captured from systemd's varlinkctl v255)"
$ varlinkctl info /tmp/queued.sock
    Vendor: sdvarlink examples
   Product: queued
   Version: 1.0.0
       URL: https://github.com/rossedman/sdvarlink/tree/main/examples/queued
Interfaces: io.example.Queue
            org.varlink.service

$ varlinkctl call /tmp/queued.sock io.example.Queue.Enqueue '{"command":"make test","priority":"high"}'
{"job":{"id":1,"command":"make test","priority":"high"}}

$ varlinkctl call /tmp/queued.sock io.example.Queue.Enqueue '{"command":"x","priority":"urgent"}'
Method call io.example.Queue.Enqueue() failed: org.varlink.service.InvalidParameter
{"parameter":"priority"}

$ varlinkctl call /tmp/queued.sock io.example.Queue.Watch '{}'
Method call io.example.Queue.Watch() failed: org.varlink.service.ExpectedMore
{}

$ varlinkctl call --more /tmp/queued.sock io.example.Queue.Watch '{"replay":true}'
{"job":{"id":1,"command":"make test","priority":"high"}}
…one line per job as it is enqueued, until Ctrl-C
```

## The interface

```text title="examples/queued/io.example.Queue.varlink"
--8<-- "examples/queued/io.example.Queue.varlink"
```

## The code

```go title="examples/queued/main.go" linenums="1"
--8<-- "examples/queued/main.go"
```

## How it works

- **The IDL is embedded** with `//go:embed` and registered with
  `srv.Interface`, so `varlinkctl introspect` shows exactly the file above.
- **Validation is free.** The server rejects the unknown enum value `urgent`,
  a missing `command`, or an extra field before the handler runs. It answers
  `InvalidParameter` and names the field, the same way sd-varlink does.
- **`Enqueue` authorizes on the peer's UID** via `call.Peer()`, and returns
  `varlink.ErrPermissionDenied` to everyone except root and the user running
  the service.
- **`QueueFullError` is a typed error.** It has a `VarlinkError()` method, so
  the client receives `io.example.Queue.QueueFull` with `{"capacity": N}`.
- **`Watch` is a live stream.** Each `send` goes out immediately, and the
  handler returns when its `ctx` ends: when the client disconnects or when the
  server shuts down.
- **Socket activation first.** Under a `.socket` unit, `ListenActivated` adopts
  systemd's socket; from a terminal, `Listen` binds `-socket` itself.

## See also

- [Serving Varlink](../guides/server.md)
- [Publish metrics](metrics-provider.md)
