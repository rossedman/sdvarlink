---
description: Frequently asked questions about sdvarlink.
---

# FAQ

??? question "Do I need cgo, libsystemd or `varlinkctl` installed?"

    No. sdvarlink is pure Go and speaks the protocol itself over Unix sockets.
    Binaries build with `CGO_ENABLED=0`. `varlinkctl` is handy for poking at
    services, but not required.

??? question "Which systemd versions are supported?"

    v257 and newer. Bindings are generated for v257, v258 and v260, and
    `systemd/latest` points at v260. On older systemd, a service's socket
    doesn't exist and `Dial` returns an error matching `varlink.ErrUnsupported`,
    which is your cue to fall back to D-Bus. See
    [Versioning & feature detection](../guides/versioning.md).

??? question "Why is there no v259 package?"

    sdvarlink generates bindings for selected releases (v257, v258, v260). Each
    package works against any newer systemd too, because newer releases accept
    older calls and unknown reply fields land in `Extra`. For v259 hosts, import
    `v258` packages (or `v260` and feature-detect what's new).

??? question "Should I import `systemd/v257/...` or `systemd/latest/...`?"

    Libraries and daemons that run on many hosts: the **oldest** release you
    support, plus `Supports` for anything newer. Tools for a known fleet:
    `latest` is fine. Details in [Versioning](../guides/versioning.md#versioned-packages).

??? question "Does it work on macOS or in CI without systemd?"

    The core, the IDL parser and the `varlinktest` fake build and run on macOS
    (and other Unix systems), so unit tests built on fakes run anywhere. Talking
    to systemd itself requires Linux. Recorded golden files
    ([record & replay](../guides/testing.md#record-and-replay)) let CI exercise
    real systemd responses without systemd.

??? question "Is a client safe for concurrent use?"

    Yes. Clients from `pkg.Dial` and `pkg.DialPath` wrap a connection pool. A
    single `varlink.Conn` serializes calls, as the protocol requires. See
    [Concurrency & pools](../guides/concurrency.md).

??? question "How do I cancel a call?"

    Cancel its context, or `break` out of a stream. Varlink can't cancel a call
    on the server, so sdvarlink closes that connection; pools discard it and
    dial a fresh one for the next call.

??? question "My call fails with `permission denied` or `PermissionDenied`."

    systemd authorizes every call by your process's credentials and polkit.
    Run as root, add a polkit rule, or set `AllowInteractiveAuthentication:
    varlink.Ptr(true)` on methods that support it when running from a terminal.
    See the [Security model](../guides/security.md).

??? question "A newer systemd sends fields my bindings don't know. Are they lost?"

    No. Every generated struct has an `Extra map[string]json.RawMessage` holding
    unknown fields. See [Typed clients: unknown fields](../guides/typed-clients.md#unknown-fields-extra).

??? question "A reply fails to decode with a JSON type error."

    A field changed type between systemd releases (rare, but it happens, e.g.
    `io.systemd.Hostname`'s `OperatingSystemImageID` was `?int` in v258's IDL
    and `?string` in v260's). Use the package of the release whose types match,
    or decode that method with a raw call. See
    [Versioning: when a field changes type](../guides/versioning.md#when-a-field-changes-type).

??? question "Can I use sdvarlink for my own Varlink services?"

    Yes, on both sides. The `varlink` core **calls** any Varlink service, and
    `varlink.Server` **serves** your own interfaces (or systemd's, such as
    `io.systemd.Metrics`) with typed handlers; see
    [Serving Varlink](../guides/server.md). `sdvarlink-gen` can generate
    client bindings from your own IDL with a custom `generate.json`, and
    `varlinktest` can *fake* any interface in tests.

??? question "Does it support TCP or remote hosts?"

    No, by design. systemd's Varlink services listen on local Unix sockets.
    For remote access, use a bridge such as varlink-http-bridge with its own
    authentication.

??? question "How do I start or stop a unit?"

    Not over Varlink yet: systemd's `io.systemd.Unit` offers `List` and
    `SetProperties`, but unit lifecycle control is still D-Bus. Use
    go-systemd's `dbus` package for that. See
    [When to keep D-Bus](../guides/migrating-from-dbus.md#when-to-keep-d-bus).

??? question "How is this different from other Go Varlink libraries?"

    Other libraries are protocol layers. sdvarlink adds curated, generated,
    versioned bindings for systemd, first-class file-descriptor passing in both
    directions, streaming as Go iterators, typed errors and a fake server for
    tests. The core is purpose-built because fd passing had to be designed in.
    See [Design](design.md).

??? question "What does the name mean?"

    **s**ystem**d** + **varlink**.
