---
description: Move Go code from go-systemd and godbus D-Bus calls to sdvarlink, with side-by-side examples — and know when to keep D-Bus.
---

# Migrating from go-systemd D-Bus

sdvarlink doesn't replace [`coreos/go-systemd`](https://github.com/coreos/go-systemd)
or [`godbus`](https://github.com/godbus/dbus). It sits beside them. Use Varlink
where systemd offers it and it helps you (no broker, streaming, fd passing,
newer APIs); keep D-Bus where it's still the only, or the better, interface.

## At a glance

| | D-Bus (go-systemd / godbus) | Varlink (sdvarlink) |
| --- | --- | --- |
| Transport | Unix socket to `dbus-daemon` (or `dbus-broker`) | Unix socket straight to the service |
| Broker | Required | None: works in early boot, initrd, minimal containers |
| Encoding | D-Bus binary type system (`a(iiay)`, variants) | JSON |
| API shape | Objects, interfaces, properties, signals | Methods with typed in/out, errors |
| Streaming | Signals (subscribe, then match) | `more` replies → `iter.Seq2` |
| File descriptors | `UNIX_FD` type | `SCM_RIGHTS` with index fields → `*os.File` |
| Errors | Error name + message strings | Error name + typed parameters → Go types |
| Introspection | XML | Varlink IDL (`idl.Parse`) |
| Debugging | `busctl` | `varlinkctl` |
| Authorization | Bus policy + polkit | `SO_PEERCRED` + polkit |
| Availability | Every systemd | v257+ per interface (see [Default sockets](../reference/sockets.md)) |
| Go typing | Mostly hand-written (`go-systemd`) or untyped (`godbus`) | Generated from systemd's IDL |

## Side by side

### Read the hostname and OS

=== "D-Bus (godbus)"

    <!-- check:skip -->
    ```go
    conn, err := dbus.ConnectSystemBus()
    if err != nil {
        return err
    }
    defer conn.Close()

    obj := conn.Object("org.freedesktop.hostname1", "/org/freedesktop/hostname1")
    host, err := obj.GetProperty("org.freedesktop.hostname1.Hostname")
    if err != nil {
        return err
    }
    osName, err := obj.GetProperty("org.freedesktop.hostname1.OperatingSystemPrettyName")
    if err != nil {
        return err
    }
    fmt.Println(host.Value().(string), osName.Value().(string)) // type assertions on variants
    ```

=== "Varlink (sdvarlink)"

    ```go
    c, err := hostname.Dial(ctx)
    if err != nil {
        return err
    }
    defer c.Close()

    h, err := c.Describe(ctx, hostname.DescribeIn{}) // one call, every property, typed
    if err != nil {
        return err
    }
    fmt.Println(h.Hostname, varlink.Deref(h.OperatingSystemPrettyName))
    ```

### Resolve a hostname

=== "D-Bus (godbus)"

    <!-- check:skip -->
    ```go
    conn, err := dbus.ConnectSystemBus()
    if err != nil {
        return err
    }
    defer conn.Close()

    var (
        addrs []struct {
            Ifindex int32
            Family  int32
            Address []byte
        }
        canonical string
        flags     uint64
    )
    obj := conn.Object("org.freedesktop.resolve1", "/org/freedesktop/resolve1")
    err = obj.CallWithContext(ctx, "org.freedesktop.resolve1.Manager.ResolveHostname", 0,
        int32(0), "systemd.io", int32(syscall.AF_UNSPEC), uint64(0)).
        Store(&addrs, &canonical, &flags)
    if err != nil {
        // err is a dbus.Error; compare err.Name against strings
        return err
    }
    for _, a := range addrs {
        ip, _ := netip.AddrFromSlice(a.Address)
        fmt.Println(canonical, ip)
    }
    ```

=== "Varlink (sdvarlink)"

    ```go
    c, err := resolve.Dial(ctx)
    if err != nil {
        return err
    }
    defer c.Close()

    out, err := c.ResolveHostname(ctx, resolve.ResolveHostnameIn{Name: "systemd.io"})
    if errors.Is(err, resolve.ErrNoSuchResourceRecord) { // typed errors
        return fmt.Errorf("no such host")
    }
    if err != nil {
        return err
    }
    for _, a := range out.Addresses {
        b := make([]byte, len(a.Address))
        for i, v := range a.Address {
            b[i] = byte(v)
        }
        ip, _ := netip.AddrFromSlice(b)
        fmt.Println(out.Name, ip)
    }
    ```

### List units

=== "D-Bus (go-systemd)"

    <!-- check:skip -->
    ```go
    conn, err := sddbus.NewSystemConnectionContext(ctx) // github.com/coreos/go-systemd/v22/dbus
    if err != nil {
        return err
    }
    defer conn.Close()

    units, err := conn.ListUnitsContext(ctx) // all units, in one reply
    if err != nil {
        return err
    }
    for _, u := range units {
        fmt.Println(u.Name, u.LoadState, u.ActiveState, u.SubState, u.Description)
    }
    ```

=== "Varlink (sdvarlink, systemd v258+)"

    ```go
    c, err := unit.Dial(ctx)
    if err != nil {
        return err
    }
    defer c.Close()

    // One reply per unit, streamed; Context and Runtime carry far more
    // detail than ListUnits (cgroup, exec settings, timestamps, ...).
    for u, err := range c.ListMore(ctx, unit.ListIn{}) {
        if err != nil {
            return err
        }
        fmt.Println(u.Context.ID,
            varlink.Deref(u.Runtime.LoadState), varlink.Deref(u.Runtime.ActiveState),
            varlink.Deref(u.Runtime.SubState), varlink.Deref(u.Context.Description))
    }
    ```

### List users: Varlink only

There is no D-Bus API for the user database: `systemd-userdbd`'s
multiplexer, which merges `/etc/passwd`, `systemd-homed`, LDAP via NSS and
more, is Varlink-only. With D-Bus your only option was NSS (`os/user`), which
can't enumerate:

```go
c, err := userdatabase.Dial(ctx)
if err != nil {
    return err
}
defer c.Close()

for rec, err := range c.GetUserRecordMore(ctx, userdatabase.GetUserRecordIn{Service: "io.systemd.Multiplexer"}) {
    if err != nil {
        return err
    }
    fmt.Printf("%s\n", rec.Record) // a full JSON user record (systemd.io/USER_RECORD)
}
```

## A migration pattern

Migrate one call site at a time behind a small interface, with Varlink as
the preferred path and D-Bus as the fallback:

```go
// HostInfo is what the rest of the program needs.
type HostInfo struct {
    Hostname string
    OS       string
}

// GetHostInfo prefers Varlink and falls back to D-Bus on older systemd.
func GetHostInfo(ctx context.Context) (*HostInfo, error) {
    c, err := hostname.Dial(ctx)
    if errors.Is(err, varlink.ErrUnsupported) {
        return hostInfoDBus(ctx) // your existing go-systemd/godbus code
    }
    if err != nil {
        return nil, err
    }
    defer c.Close()

    h, err := c.Describe(ctx, hostname.DescribeIn{})
    if err != nil {
        return nil, err
    }
    return &HostInfo{Hostname: h.Hostname, OS: varlink.Deref(h.OperatingSystemPrettyName)}, nil
}

func hostInfoDBus(ctx context.Context) (*HostInfo, error) {
    return nil, errors.New("not shown: the existing D-Bus implementation")
}
```

Then:

1. Ship with both paths and log which one ran.
2. Replace D-Bus-specific tests with [`varlinktest`](testing.md) fakes, which
   are far easier to set up than a private bus.
3. Delete the fallback once your fleet's oldest systemd has the interface.

## When to keep D-Bus

Varlink coverage in systemd is growing but not complete. Keep D-Bus for:

- **Starting, stopping and restarting units, and jobs.** `io.systemd.Unit` has
  `List` (v258+) and `SetProperties` (v260+); `io.systemd.Manager` has
  `Describe`, `Reload`, `Reexecute` and `EnqueueMarkedJobs`. Unit lifecycle
  control (`StartUnit`, `StopUnit`, ...) and transient units are D-Bus.
- **Property-change signals.** `PropertiesChanged` subscriptions on units have
  no general Varlink equivalent yet. (Some services stream instead:
  `io.systemd.Resolve.Monitor`, `io.systemd.ManagedOOM`.)
- **logind sessions, seats and inhibitors.** `io.systemd.Login` covers
  `CreateSession` / `ReleaseSession` for PAM; most of `org.freedesktop.login1`
  is D-Bus.
- **Hosts older than systemd v257**, or where a particular service isn't
  running.

Check the [interface reference](../reference/interfaces.md) for what each
release offers, and the [API changes](../reference/api-changes.md) page to see
the surface growing.

## Gotchas when porting

| D-Bus habit | Varlink equivalent |
| --- | --- |
| Read one property at a time | Methods like `Describe` and `List` return the whole object at once. |
| Byte arrays (`ay`) for addresses | JSON arrays of numbers → `[]int64`; convert with a small loop. |
| Compare `dbus.Error.Name` strings | `errors.Is(err, pkg.Err<Name>)` / `errors.As` with generated types. |
| Subscribe to signals, then call | Call with `more` and range over the iterator. |
| Object paths (`/org/freedesktop/systemd1/unit/ssh_2eservice`) | Plain names and IDs in parameters (`unit.ListIn{Name: varlink.Ptr("ssh.service")}`). |
| One shared bus connection | One socket per service; generated clients pool connections per service. |
