---
description: Stream every user record from systemd's userdb multiplexer, like userdbctl user.
---

# List users

:material-github: [Source](https://github.com/rossedman/sdvarlink/tree/main/examples/list-users) · :material-api: [`io.systemd.UserDatabase`](../reference/interfaces/userdatabase.md) · :material-tag-outline: systemd v257+ · :material-shield-account-outline: Unprivileged

`userdbctl user` in Go. `systemd-userdbd`'s multiplexer merges every user
source on the system (`/etc/passwd`, `systemd-homed`, NSS, ...) and, with
Varlink's `more` flag, streams one reply per user. There is no D-Bus equivalent.

## Run it

```sh
go run ./examples/list-users
go run ./examples/list-users -user root
```

```text title="Output (illustrative)"
NAME             UID    DISPOSITION  HOME               REAL NAME
root             0      intrinsic    /root              Super User
nobody           65534  intrinsic    /                  Kernel Overflow User
systemd-resolve  991    system       /                  systemd Resolver
ross             1000   regular      /home/ross         Ross Edman
```

## The code

```go title="examples/list-users/main.go" linenums="1" hl_lines="36 48 58"
--8<-- "examples/list-users/main.go"
```

## How it works

- **Supports-more.** `GetUserRecord` is annotated `[Supports 'more' flag]`, so
  the package has both `GetUserRecord` (one reply, used with `-user`) and
  `GetUserRecordMore` (an `iter.Seq2` of every record).
- **`Service: "io.systemd.Multiplexer"`** is required: it tells userdbd to
  query all sources.
- **The record is JSON.** `Record` is `json.RawMessage` holding a full
  [JSON user record](https://systemd.io/USER_RECORD/); the example decodes a few
  fields into its own struct.
- **`varlink.Ptr`** builds the optional `UserName` filter.

## See also

- [Streaming with iterators](../guides/streaming.md)
- [Testing streaming fakes](../guides/testing.md#streaming-fakes)
