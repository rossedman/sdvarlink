---
description: Task-focused guides for sdvarlink, each with runnable code.
---

# Guides

Each guide covers one task end to end, with runnable code you can paste into
a program or test. If you're new, read [Core concepts](../getting-started/concepts.md)
first; it takes five minutes and makes everything here click.

<div class="grid cards" markdown>

-   :material-language-go:{ .lg .middle } **[Typed clients](typed-clients.md)**

    ---

    Anatomy of a generated package: dialing, inputs and outputs, optional
    fields, enums, unknown fields and escaping to raw calls.

-   :material-waves:{ .lg .middle } **[Streaming with iterators](streaming.md)**

    ---

    `iter.Seq2` for `more` calls: collecting, filtering, breaking early,
    timeouts and long-lived subscriptions.

-   :material-file-swap:{ .lg .middle } **[File descriptors](file-descriptors.md)**

    ---

    Send and receive `*os.File` over `SCM_RIGHTS`, ownership rules, limits
    and the mount-image walkthrough.

-   :material-alert-octagon-outline:{ .lg .middle } **[Errors](errors.md)**

    ---

    Typed errors and sentinels, `syscall.Errno` from `io.systemd.System`,
    protocol errors, and falling back when a service is missing.

-   :material-source-branch:{ .lg .middle } **[Versioning & feature detection](versioning.md)**

    ---

    Pinned vs `latest` packages, `Supports`, and handling API changes
    between systemd releases.

-   :material-lan-connect:{ .lg .middle } **[Concurrency & pools](concurrency.md)**

    ---

    One call per connection, `Pool`, fan-out patterns, contexts and
    deadlines.

-   :material-console:{ .lg .middle } **[Raw calls & the core package](raw-calls.md)**

    ---

    `json.RawMessage` calls, `CallReply`, `WithFiles`, introspection and the
    IDL parser.

-   :material-test-tube:{ .lg .middle } **[Testing with varlinktest](testing.md)**

    ---

    Typed fake handlers, streaming fakes, fd fixtures, call assertions and
    record/replay against real systemd.

-   :material-swap-horizontal:{ .lg .middle } **[Migrating from go-systemd D-Bus](migrating-from-dbus.md)**

    ---

    Side-by-side code for common tasks, and when to keep D-Bus.

-   :material-shield-lock-outline:{ .lg .middle } **[Security model](security.md)**

    ---

    Who authorizes calls, which limits protect you, and how fds and unknown
    data are handled.

</div>
