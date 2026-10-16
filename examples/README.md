# Examples

Each directory is a runnable program (`go run ./examples/<name>`) or, for
the testing examples, a package with tests (`go test ./examples/<name>`).
Programs that talk to systemd need the matching service running; several
need root.

| Example | Shows | systemd interface |
| --- | --- | --- |
| [resolve-hostname](resolve-hostname/) | A typed call and a typed error | `io.systemd.Resolve` |
| [concurrent-lookups](concurrent-lookups/) | Many goroutines sharing one pooled client | `io.systemd.Resolve` |
| [list-users](list-users/) | A single call and a streamed `…More` call | `io.systemd.UserDatabase` |
| [list-units](list-units/) | Streaming, plus `Supports` for a v258+ method | `io.systemd.Unit` |
| [machines](machines/) | Streaming, and an error that means "empty" | `io.systemd.Machine` |
| [hostname](hostname/) | Optional reply fields with `varlink.Deref` | `io.systemd.Hostname` |
| [journal-control](journal-control/) | Methods without parameters or replies | `io.systemd.Journal` |
| [dns-monitor](dns-monitor/) | A long-lived stream stopped by Ctrl-C | `io.systemd.Resolve.Monitor` |
| [mount-image](mount-image/) | Passing a file descriptor in, receiving several out | `io.systemd.MountFileSystem` |
| [metrics](metrics/) | Services without a fixed socket (`DialPath`) | `io.systemd.Metrics` |
| [feature-detect](feature-detect/) | `ErrUnsupported` and `Supports` across releases | several |
| [introspect](introspect/) | `GetInfo`, `GetInterfaceDescription` and the `idl` parser | any |
| [raw-call](raw-call/) | The core package alone: a tiny `varlinkctl call` | any |
| [testing-with-fake](testing-with-fake/) | Unit tests against `varlinktest` | `io.systemd.Resolve` |
| [record-replay](record-replay/) | Golden files recorded from a real journald | `io.systemd.Journal` |
