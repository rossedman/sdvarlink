---
description: How generated sdvarlink packages are laid out, and how to dial, call, and read replies with them.
---

# Typed clients

Every `io.systemd.*` interface gets its own generated Go package. This guide
walks through what's in one and how to use it.

## Anatomy of a generated package

Take `io.systemd.Resolve`, generated into
`github.com/rossedman/sdvarlink/systemd/v257/resolve`:

<!-- check:skip -->
```go
package resolve

const (
    InterfaceName  = "io.systemd.Resolve"                      // the Varlink interface
    SystemdVersion = 257                                       // release the bindings come from
    DefaultSocket  = "/run/systemd/resolve/io.systemd.Resolve" // where systemd serves it
)

var InterfaceDescription string // the IDL, embedded (for fakes and tooling)

type Client struct{ /* ... */ }

func New(c varlink.Caller) *Client
func Dial(ctx context.Context, opts ...varlink.Option) (*Client, error)
func DialPath(ctx context.Context, path string, opts ...varlink.Option) (*Client, error)

func (c *Client) Close() error
func (c *Client) Caller() varlink.Caller
func (c *Client) Supports(ctx context.Context, method string) (bool, error)

// One method per IDL method:
func (c *Client) ResolveHostname(ctx context.Context, in ResolveHostnameIn) (*ResolveHostnameOut, error)
func (c *Client) ResolveAddress(ctx context.Context, in ResolveAddressIn) (*ResolveAddressOut, error)
// ...

// Types: ResolveHostnameIn, ResolveHostnameOut, ResolvedAddress, ...
// Errors: NoSuchResourceRecordError + ErrNoSuchResourceRecord, ...
```

| Generated item | From the IDL | Example |
| --- | --- | --- |
| `<Method>In` | method parameters | `ResolveHostnameIn` |
| `<Method>Out` | method reply | `ResolveHostnameOut` |
| `<Type>` | `type Name (...)` | `ResolvedAddress` |
| `<Type>` + constants | enum `type Name (a, b)` | `MachineOpenMode`, `MachineOpenModeTTY` |
| `<Parent><Field>` | inline struct or enum in a field | `limit: (soft: int, hard: int)` in `GetOut` → `GetOutLimit` |
| `<Name>Error` + `Err<Name>` | `error Name (...)` | `NoSuchResourceRecordError`, `ErrNoSuchResourceRecord` |

The full rules live in [Type mapping & naming](../reference/type-mapping.md).
Each interface's methods, types and errors are listed in the
[interface reference](../reference/interfaces.md).

## Connecting

=== "Dial (default socket)"

    ```go
    c, err := resolve.Dial(ctx)
    if err != nil {
        return err // matches varlink.ErrUnsupported if the socket is missing
    }
    defer c.Close()
    ```

    `Dial` connects to `DefaultSocket`. It dials once up front, so a missing
    service is reported right here rather than on the first call. If the
    socket doesn't exist, or you're not on Linux, the error matches
    `varlink.ErrUnsupported`.

=== "DialPath (any socket)"

    ```go
    // A test fake, a container's socket, or a service at a custom path.
    c, err := resolve.DialPath(ctx, "/var/lib/machines/web/run/systemd/resolve/io.systemd.Resolve")
    if err != nil {
        return err
    }
    defer c.Close()
    ```

    Interfaces without one well-known socket (such as `io.systemd.Metrics`,
    served by many daemons under `/run/systemd/report/`) only have `DialPath`.

=== "New (bring your own Caller)"

    ```go
    // Share one pool across clients, tune it, or use a single connection.
    pool := varlink.NewPool(func(ctx context.Context) (*varlink.Conn, error) {
        return varlink.DialService(ctx, resolve.DefaultSocket)
    }, varlink.WithMaxIdle(16))
    defer pool.Close()

    c := resolve.New(pool) // Close on c is a no-op: you own the pool
    ```

    `New` accepts anything implementing `varlink.Caller`: a `*varlink.Conn`,
    a `*varlink.Pool`, or your own test double.

Connection options apply to every connection the client opens:

```go
c, err := mountfilesystem.Dial(ctx,
    varlink.WithMaxFrameSize(32<<20), // accept replies up to 32 MiB
    varlink.WithMaxFiles(64),         // accept up to 64 fds per read
)
```

A client is **safe for concurrent use**: `Dial` and `DialPath` give it a pool
of connections. See [Concurrency & pools](concurrency.md).

## Calling methods

Methods mirror the IDL. Their shape depends on whether the method takes
parameters and returns any:

=== "Parameters → reply"

    <!-- check: var c *resolve.Client -->
    ```go
    out, err := c.ResolveHostname(ctx, resolve.ResolveHostnameIn{Name: "go.dev"})
    if err != nil {
        return err
    }
    fmt.Println(out.Name, len(out.Addresses))
    ```

=== "No parameters"

    ```go
    // io.systemd.Manager.Describe() -> (context, runtime)
    m, err := manager.Dial(ctx)
    if err != nil {
        return err
    }
    defer m.Close()

    d, err := m.Describe(ctx)
    if err != nil {
        return err
    }
    fmt.Println("systemd", d.Runtime.Version, "on", d.Runtime.Architecture)
    ```

=== "No reply fields"

    ```go
    // io.systemd.Journal.Rotate() -> ()
    j, err := journal.Dial(ctx)
    if err != nil {
        return err
    }
    defer j.Close()

    if err := j.Rotate(ctx); err != nil { // just an error
        return err
    }
    ```

Methods that stream get an iterator variant; see
[Streaming with iterators](streaming.md).

## Optional fields: `Ptr` and `Deref`

Nullable IDL types (`?string`, `?int`, `?bool`) become pointers tagged
`omitempty`, so "not set" and "zero" stay distinct on the wire. Required
fields are values and are always sent.

```go
in := resolve.ResolveHostnameIn{
    Name:    "example.com",            // required: string
    Family:  varlink.Ptr(int64(10)),   // optional: *int64 (AF_INET6)
    Ifindex: varlink.Ptr(int64(2)),    // optional: *int64
}
// Sent as {"name":"example.com","family":10,"ifindex":2}; Flags is omitted.
```

On the way back, read optional fields with `varlink.Deref`, which returns the
zero value for `nil`:

<!-- check: var c *hostname.Client -->
```go
h, err := c.Describe(ctx, hostname.DescribeIn{})
if err != nil {
    return err
}
fmt.Println(varlink.Deref(h.PrettyHostname)) // "" if unset
if h.Chassis != nil {
    fmt.Println("chassis:", *h.Chassis)
}
```

Optional arrays, maps and `object` fields aren't pointers: a nil slice or map
already means "absent", so they're plain `[]T` / `map[string]T` /
`json.RawMessage` with `omitempty`.

## Enums

IDL enums become named string types with a constant per value and a
`String()` method:

<!-- check: var c *machine.Client -->
```go
// io.systemd.Machine: type MachineOpenMode (tty, login, shell)
out, err := c.Open(ctx, machine.OpenIn{
    Name: varlink.Ptr("web"),
    Mode: machine.MachineOpenModeLogin,
})
if err != nil {
    return err
}
defer out.PtyFileDescriptor.Close() // a pty fd, passed back over the socket
fmt.Println("pty:", out.PtyPath)
```

Because they're strings underneath, values a newer systemd adds still decode
fine; compare against the constants you know and handle the rest.

## Unknown fields: `Extra`

Every generated struct has an `Extra map[string]json.RawMessage` field. When a
newer systemd sends a field these bindings don't know, it lands in `Extra`
instead of being silently dropped:

<!-- check: var c *resolve.Client -->
```go
out, err := c.ResolveHostname(ctx, resolve.ResolveHostnameIn{Name: "example.com"})
if err != nil {
    return err
}
for name, raw := range out.Extra {
    fmt.Printf("field from a newer systemd: %s = %s\n", name, raw)
}
```

!!! note

    `Extra` is filled when **decoding** and sent when **encoding**, so a
    struct round-trips unchanged. You can also use it to send a field your
    bindings don't know yet, such as a parameter added in a newer systemd:
    `in.Extra = map[string]json.RawMessage{"newField": json.RawMessage("true")}`.
    Fields the struct sets itself take precedence over `Extra`.

## Embedded IDL

Each package embeds the IDL it was generated from as `InterfaceDescription`.
Hand it to a fake so introspection works in tests, or parse it for tooling:

```go
iface, err := idl.Parse(resolve.InterfaceDescription)
if err != nil {
    return err
}
for _, m := range iface.Methods {
    fmt.Printf("%-24s %s\n", m.Name, m.More) // e.g. "BrowseServices  requires-more"
}
```

## Escaping to raw calls

The bindings cover what systemd declares in its IDL. For anything else (an
experimental method, a vendor extension, a field the bindings don't know),
`Client.Caller()` returns the underlying `varlink.Caller`, which speaks raw
JSON:

<!-- check: var c *resolve.Client -->
```go
var out json.RawMessage
err := c.Caller().Call(ctx, "io.systemd.Resolve.ResolveHostname",
    json.RawMessage(`{"name":"example.com","flags":0,"someFutureFlag":true}`), &out)
if err != nil {
    return err
}
fmt.Println(string(out))
```

More in [Raw calls & the core package](raw-calls.md).

## Method-name collisions

`Client` already has `Close`, `Caller` and `Supports`. If an interface
declares a method with one of those names, the generated method gets a
`Method` suffix (e.g. a hypothetical `Close` becomes `CloseMethod`). Other
collisions between generated identifiers get a trailing underscore.

## See also

- [Examples: Describe the host](../examples/hostname.md), [Resolve a hostname](../examples/resolve-hostname.md)
- [Interface reference](../reference/interfaces.md)
- [`pkg.go.dev/github.com/rossedman/sdvarlink/systemd/latest/resolve`](https://pkg.go.dev/github.com/rossedman/sdvarlink/systemd/latest/resolve)
