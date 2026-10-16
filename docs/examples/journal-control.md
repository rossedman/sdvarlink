---
description: Ask systemd-journald to sync, rotate, flush or relinquish, like journalctl --sync/--rotate/--flush.
---

# Journal control

:material-github: [Source](https://github.com/rossedman/sdvarlink/tree/main/examples/journal-control) · :material-api: [`io.systemd.Journal`](../reference/interfaces/journal.md) · :material-tag-outline: systemd v257+ · :material-shield-account-outline: Root

Drive `systemd-journald` the way `journalctl --sync`, `--rotate`, `--flush`
and `--relinquish-var` do. Every method takes no parameters and returns no
fields, so each generated method is just `func(ctx) error`.

## Run it

```sh
sudo go run ./examples/journal-control sync
```

```text title="Output (real, systemd-journald v255)"
2026/10/16 00:10:09 journald: sync done
```

## The code

```go title="examples/journal-control/main.go" linenums="1" hl_lines="24 32 34"
--8<-- "examples/journal-control/main.go"
```

## How it works

- **No input, no output.** For `method Rotate() -> ()` the generator emits
  `Rotate(ctx context.Context) error`.
- **Root only.** journald's socket is mode `0600`, so an unprivileged run
  fails at `Dial` with `permission denied`.
- **Version differences.** This example pins v257. From v258,
  `Synchronize` takes a `SynchronizeIn` with an optional `Offline` flag; see
  [Versioning](../guides/versioning.md#a-parameter-is-added).
- **Timeouts.** Rotating and flushing touch disk; the 30-second context keeps a
  wedged journald from hanging the tool.

## See also

- [Versioning: a parameter is added](../guides/versioning.md#a-parameter-is-added)
- [Record & replay (journald)](record-replay.md)
