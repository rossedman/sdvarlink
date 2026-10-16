---
description: Record a session against real systemd-journald into a golden file and replay it in CI.
---

# Record & replay

:material-github: [Source](https://github.com/rossedman/sdvarlink/tree/main/examples/record-replay) · :material-api: [`io.systemd.Journal`](../reference/interfaces/journal.md) · :material-shield-account-outline: Root to record; none to replay

Golden-file testing against real systemd. `health.go` checks that journald
answers over Varlink. Its test **replays** `testdata/journal.json`, recorded
from a real `systemd-journald` v255, so it runs anywhere. Pass `-update` on a
systemd host to **record** a fresh golden file through a forwarding fake.

## Run it

```sh
go test -v ./examples/record-replay/                # replay
sudo go test ./examples/record-replay/ -update      # record against journald
```

```text title="Output (real)"
=== RUN   TestCheckJournal
--- PASS: TestCheckJournal (0.00s)
PASS
ok  	github.com/rossedman/sdvarlink/examples/record-replay	0.006s
```

## The code

=== "health.go"

    ```go title="examples/record-replay/health.go" linenums="1"
    --8<-- "examples/record-replay/health.go"
    ```

=== "health_test.go"

    ```go title="examples/record-replay/health_test.go" linenums="1"
    --8<-- "examples/record-replay/health_test.go"
    ```

=== "testdata/journal.json"

    ```json title="examples/record-replay/testdata/journal.json" linenums="1"
    --8<-- "examples/record-replay/testdata/journal.json"
    ```

## How it works

- **`varlinktest.Record(t, journal.DefaultSocket, golden)`** starts a fake that
  forwards every call to the real journald and writes the exchanges to the
  golden file when the test ends.
- **`varlinktest.Replay(t, golden)`** answers from the file, failing the test
  if calls arrive out of order or with different parameters.
- **The code under test doesn't know.** It gets a `*journal.Client` dialed at
  `srv.Path()` either way.
- **Reviewable fixtures.** The golden file is indented JSON: one entry per
  call, with its parameters and replies.

## See also

- [Testing: record and replay](../guides/testing.md#record-and-replay)
- [Testing with a fake](testing-with-fake.md)
