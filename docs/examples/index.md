---
description: Fifteen runnable sdvarlink programs, from resolvectl query to mounting disk images over file descriptors.
---

# Examples

Fifteen small, runnable programs live in [`examples/`](https://github.com/rossedman/sdvarlink/tree/main/examples). Each one is a
complete `main` package (or a package with tests) that compiles against the
current API in CI. Clone the repository and run any of them with `go run`:

```sh
git clone https://github.com/rossedman/sdvarlink && cd sdvarlink
go run ./examples/hostname
```

!!! tip "No systemd handy?"

    The two testing examples run anywhere: `go test ./examples/testing-with-fake/ ./examples/record-replay/`.

## Queries

Single calls with typed inputs and outputs.

<div class="grid cards" markdown>

-   :material-dns-outline:{ .lg .middle } **[Resolve a hostname](resolve-hostname.md)**

    ---

    Resolve names through systemd-resolved's io.systemd.Resolve interface, like resolvectl query.

    `io.systemd.Resolve` · Unprivileged

-   :material-server-outline:{ .lg .middle } **[Describe the host](hostname.md)**

    ---

    Print what systemd-hostnamed knows about the machine, like hostnamectl.

    `io.systemd.Hostname` · Unprivileged

-   :material-lan-connect:{ .lg .middle } **[Concurrent lookups](concurrent-lookups.md)**

    ---

    Resolve many names in parallel with one pooled generated client.

    `io.systemd.Resolve` · Unprivileged

-   :material-notebook-outline:{ .lg .middle } **[Journal control](journal-control.md)**

    ---

    Ask systemd-journald to sync, rotate, flush or relinquish, like journalctl --sync/--rotate/--flush.

    `io.systemd.Journal` · Root

</div>

## Streaming

Methods that answer with many replies, as Go iterators.

<div class="grid cards" markdown>

-   :material-account-multiple-outline:{ .lg .middle } **[List users](list-users.md)**

    ---

    Stream every user record from systemd's userdb multiplexer, like userdbctl user.

    `io.systemd.UserDatabase` · Unprivileged

-   :material-format-list-bulleted:{ .lg .middle } **[List units](list-units.md)**

    ---

    List units through PID 1's io.systemd.Unit interface (systemd v258+), with feature detection.

    `io.systemd.Unit` · Unprivileged

-   :material-cube-outline:{ .lg .middle } **[List machines](machines.md)**

    ---

    List containers and VMs registered with systemd-machined, like machinectl list.

    `io.systemd.Machine` · Unprivileged

-   :material-radar:{ .lg .middle } **[Monitor DNS](dns-monitor.md)**

    ---

    Stream every DNS query systemd-resolved answers, like resolvectl monitor.

    `io.systemd.Resolve.Monitor` · Root

-   :material-chart-line:{ .lg .middle } **[Read metrics](metrics.md)**

    ---

    Read the io.systemd.Metrics interface systemd services publish under /run/systemd/report/ (v260+).

    `io.systemd.Metrics` · Unprivileged

</div>

## File descriptors

Open files over the socket, in both directions.

<div class="grid cards" markdown>

-   :material-harddisk:{ .lg .middle } **[Mount a disk image](mount-image.md)**

    ---

    Pass a disk image to systemd-mountfsd as a file descriptor and receive mount file descriptors back.

    `io.systemd.MountFileSystem` · Root or polkit

</div>

## Core & introspection

The varlink core and IDL parser, no bindings needed.

<div class="grid cards" markdown>

-   :material-magnify-scan:{ .lg .middle } **[Introspect a service](introspect.md)**

    ---

    List a Varlink service's interfaces and methods with the core package and the IDL parser.

    core · Depends on the socket

-   :material-code-json:{ .lg .middle } **[Raw calls](raw-call.md)**

    ---

    Call any Varlink method with JSON parameters and print the replies, like varlinkctl call.

    core · Depends on the socket

-   :material-check-decagram-outline:{ .lg .middle } **[Feature detection](feature-detect.md)**

    ---

    Report which Varlink services and methods the host's systemd offers, with ErrUnsupported and Supports.

    core · Unprivileged

</div>

## Servers

Serve Varlink interfaces from Go.

<div class="grid cards" markdown>

-   :material-tray-full:{ .lg .middle } **[A job queue service](queued.md)**

    ---

    A complete service with a typed call, a stream, a typed error, peer-credential checks and socket activation.

    `io.example.Queue` · Unprivileged

-   :material-chart-line:{ .lg .middle } **[Publish metrics](metrics-provider.md)**

    ---

    Serve io.systemd.Metrics with the generated types, readable by systemd tooling.

    `io.systemd.Metrics` · Unprivileged

</div>

## Testing

Test code built on sdvarlink without systemd.

<div class="grid cards" markdown>

-   :material-test-tube:{ .lg .middle } **[Testing with a fake](testing-with-fake.md)**

    ---

    Unit-test application code against a varlinktest fake io.systemd.Resolve, no systemd required.

    `io.systemd.Resolve` · None (runs anywhere)

-   :material-record-rec:{ .lg .middle } **[Record & replay](record-replay.md)**

    ---

    Record a session against real systemd-journald into a golden file and replay it in CI.

    `io.systemd.Journal` · Root to record; none to replay

</div>
