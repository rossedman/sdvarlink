---
description: Stream every DNS query systemd-resolved answers, like resolvectl monitor.
---

# Monitor DNS

:material-github: [Source](https://github.com/rossedman/sdvarlink/tree/main/examples/dns-monitor) · :material-api: [`io.systemd.Resolve.Monitor`](../reference/interfaces/resolvemonitor.md) · :material-tag-outline: systemd v257+ · :material-shield-account-outline: Root

`resolvectl monitor` in Go: subscribe to `systemd-resolved`'s query stream and
print every lookup as it completes. The subscription never ends on its own;
Ctrl-C cancels the context, which ends the stream and closes the connection.

## Run it

```sh
sudo go run ./examples/dns-monitor
```

```text title="Output (illustrative)"
monitoring… (Ctrl-C to stop)
success    github.com                               answers=1
success    api.github.com                           answers=1
success    go.dev                                   answers=8
failure    nope.invalid                             answers=0
^C
```

## The code

```go title="examples/dns-monitor/main.go" linenums="1" hl_lines="21 30 37"
--8<-- "examples/dns-monitor/main.go"
```

## How it works

- **Requires-more.** `SubscribeQueryResults` is annotated
  `[Requires 'more' flag]`, so the generated method returns the iterator
  directly; there's no single-reply variant.
- **The first reply is a handshake** with `Ready: true`, sent once the
  subscription is active.
- **Shutdown via context.** `signal.NotifyContext` cancels on Ctrl-C. The
  loop checks `ctx.Err()` first so an interrupt exits cleanly instead of
  reporting the cancellation as a failure.
- **Root only.** The monitor socket is restricted to privileged clients.

## See also

- [Streaming: long-lived subscriptions](../guides/streaming.md#long-lived-subscriptions)
- [Concurrency: contexts](../guides/concurrency.md#contexts-and-deadlines)
