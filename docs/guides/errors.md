---
description: Typed Varlink errors, Err sentinels with errors.Is, io.systemd.System errno mapping, protocol and transport errors, and falling back when a service is missing.
---

# Errors

A Varlink error reply carries a **qualified error name**, such as
`io.systemd.Resolve.NoSuchResourceRecord`, plus optional parameters. sdvarlink
turns each into a Go error you can match with `errors.Is` and `errors.As`, and
keeps transport problems clearly separate.

## The three layers

| The reply's error name | Go error you get | Match it with |
| --- | --- | --- |
| Declared by the interface (`io.systemd.Resolve.NoSuchResourceRecord`) | `*resolve.NoSuchResourceRecordError` | `errors.As(err, &e)` or `errors.Is(err, resolve.ErrNoSuchResourceRecord)` |
| `io.systemd.System` | `*varlink.SystemError` | `errors.Is(err, syscall.EACCES)`, `errors.As(err, &se)` |
| Anything else (`org.varlink.service.MethodNotFound`, names from newer releases, ...) | `*varlink.Error` | `errors.Is(err, varlink.ErrMethodNotFound)`, `varlink.ErrorName(err)` |

Problems that aren't error *replies* (no socket, a dropped connection, a
cancelled context, a malformed frame) come back as ordinary Go errors, covered
[below](#transport-and-protocol-errors).

## Typed errors

For every `error` declared in an interface's IDL, the generator emits a struct
type named `<Name>Error` and a sentinel named `Err<Name>`:

=== "IDL"

    ```text
    error NoSuchResourceRecord()
    error DNSSECValidationFailed(
        result: string,
        extendedDNSErrorCode: ?int,
        extendedDNSErrorMessage: ?string
    )
    ```

=== "Generated Go"

    <!-- check:skip -->
    ```go
    type NoSuchResourceRecordError struct{}
    var ErrNoSuchResourceRecord = &varlink.Error{Name: "io.systemd.Resolve.NoSuchResourceRecord"}

    type DNSSECValidationFailedError struct {
        Result                  string  `json:"result"`
        ExtendedDNSErrorCode    *int64  `json:"extendedDNSErrorCode,omitempty"`
        ExtendedDNSErrorMessage *string `json:"extendedDNSErrorMessage,omitempty"`
    }
    var ErrDNSSECValidationFailed = &varlink.Error{Name: "io.systemd.Resolve.DNSSECValidationFailed"}

    // Both implement error, varlink.NamedError and Is.
    ```

Use the **sentinel** when you only care *which* error it was, and the **type**
when you need its parameters:

```go
func lookup(ctx context.Context, c *resolve.Client, name string) (*resolve.ResolveHostnameOut, error) {
    out, err := c.ResolveHostname(ctx, resolve.ResolveHostnameIn{Name: name})

    var dnssec *resolve.DNSSECValidationFailedError
    switch {
    case errors.Is(err, resolve.ErrNoSuchResourceRecord):
        return nil, fmt.Errorf("%s: no such host", name)
    case errors.Is(err, resolve.ErrQueryTimedOut), errors.Is(err, resolve.ErrNoNameServers):
        return nil, fmt.Errorf("%s: DNS unavailable: %w", name, err) // retryable
    case errors.As(err, &dnssec):
        return nil, fmt.Errorf("%s: DNSSEC %s (EDE %d: %s)", name, dnssec.Result,
            varlink.Deref(dnssec.ExtendedDNSErrorCode), varlink.Deref(dnssec.ExtendedDNSErrorMessage))
    case err != nil:
        return nil, err
    }
    return out, nil
}
```

!!! tip "Sentinels match by name, wherever the error came from"

    `errors.Is` compares **error names**. So `errors.Is(err, resolve.ErrNoSuchResourceRecord)`
    is true for the typed error from a generated client *and* for a plain
    `*varlink.Error` from a [raw call](raw-calls.md) through `Client.Caller()`
    or `varlink.Conn`. A wrapped error (`fmt.Errorf("...: %w", err)`) matches too.

A typed error's message includes its name and parameters:

```text
varlink: io.systemd.Resolve.NoSuchResourceRecord
varlink: io.systemd.Resolve.DNSSECValidationFailed {"result":"invalid","extendedDNSErrorCode":6}
```

## Reading any error's name

Every Varlink error, typed or not, implements `varlink.NamedError`:

```go
// NamedError is implemented by *Error, *SystemError and every generated error type.
type NamedError interface {
    error
    VarlinkError() string // the qualified error name
}
```

`varlink.ErrorName(err)` returns that name, or `""` for non-Varlink errors.
That's handy for logging and metrics:

<!-- check: var c *resolve.Client -->
```go
_, err := c.ResolveHostname(ctx, resolve.ResolveHostnameIn{Name: "example.invalid"})
if name := varlink.ErrorName(err); name != "" {
    log.Printf("systemd-resolved said %s", name) // e.g. io.systemd.Resolve.NoSuchResourceRecord
} else if err != nil {
    log.Printf("transport error: %v", err)
}
```

## Untyped errors: `*varlink.Error`

Error names the bindings don't declare, including the standard
`org.varlink.service.*` errors, arrive as `*varlink.Error`:

```go
type Error struct {
    Name       string          // e.g. "org.varlink.service.InvalidParameter"
    Parameters json.RawMessage // raw parameters, or nil
}
```

Decode its parameters into whatever shape you expect:

<!-- check: var conn *varlink.Conn -->
```go
err := conn.Call(ctx, "io.systemd.Hostname.Describe", map[string]any{"bogus": 1}, nil)

var ve *varlink.Error
if errors.As(err, &ve) && errors.Is(err, varlink.ErrInvalidParameter) {
    var p struct {
        Parameter string `json:"parameter"`
    }
    if ve.Decode(&p) == nil {
        log.Printf("systemd rejected parameter %q", p.Parameter)
    }
}
```

## `io.systemd.System` and errno

Many systemd methods fail with the generic `io.systemd.System` error, which
carries a Linux errno. It becomes a `*varlink.SystemError` that **unwraps to
`syscall.Errno`**:

<!-- check: var c *journal.Client -->
```go
err := c.Rotate(ctx)
switch {
case errors.Is(err, syscall.EPERM), errors.Is(err, syscall.EACCES):
    return fmt.Errorf("rotating the journal needs root: %w", err)
case errors.Is(err, syscall.EROFS):
    return fmt.Errorf("journal is on a read-only filesystem: %w", err)
}

var se *varlink.SystemError
if errors.As(err, &se) {
    log.Printf("errno %d (%s) from %s", se.Errno, se.ErrnoName, se.Origin)
}
```

| Field | Meaning |
| --- | --- |
| `Errno` | The errno value; `Unwrap` returns it as `syscall.Errno` when non-zero. |
| `ErrnoName` | The symbolic name, e.g. `"EPERM"` (sent by systemd v258+). |
| `Origin` | Where the error came from, e.g. `"linux"`. |

## Core sentinels

These are exported by the `varlink` package:

| Sentinel | Kind | When you see it | Connection afterwards |
| --- | --- | --- | --- |
| `ErrUnsupported` | transport | Not Linux, or the service's socket doesn't exist (`Dial`/`DialService`/generated `Dial`). | n/a |
| `ErrClosed` | transport | Call on a closed `Conn` or `Pool`. | closed |
| `ErrFrameTooLarge` | transport | The peer sent a message over the frame limit (16 MiB by default). | closed |
| `ErrTooManyFiles` | transport | Too many fds received in one read (connection closed), or too many to send in one message (caught before sending; connection stays usable). | see cause |
| `ErrInterfaceNotFound` | reply | `org.varlink.service.InterfaceNotFound` | reusable |
| `ErrMethodNotFound` | reply | `org.varlink.service.MethodNotFound`: the method doesn't exist on this systemd. | reusable |
| `ErrMethodNotImplemented` | reply | `org.varlink.service.MethodNotImplemented` | reusable |
| `ErrInvalidParameter` | reply | `org.varlink.service.InvalidParameter`: a parameter was missing or malformed. | reusable |
| `ErrPermissionDenied` | reply | `org.varlink.service.PermissionDenied`: polkit or credentials said no. | reusable |
| `ErrExpectedMore` | reply | `org.varlink.service.ExpectedMore`: the method requires `more`. | reusable |

Plus the standard `context.Canceled` and `context.DeadlineExceeded`: when a
call is abandoned because its context ended, sdvarlink returns the context's
error rather than the I/O error it caused.

!!! note "Error replies don't break connections"

    An error **reply** is a normal end to a call: the connection stays healthy
    and the next call reuses it. Transport failures, protocol errors and
    abandoned calls close the connection; `conn.Err()` reports why, and pools
    discard it automatically.

## Transport and protocol errors

| Error | Cause |
| --- | --- |
| `*varlink.ProtocolError` | The peer sent something that isn't valid Varlink: malformed JSON, or more replies than were asked for. The connection is closed. Inspect `Msg` and `Unwrap()`. |
| `*net.OpError`, `syscall.ECONNREFUSED`, `io.EOF`, ... | Socket-level failures, e.g. the service restarted mid-call. |
| JSON decode errors | A reply didn't match the Go type, e.g. a field changed type in a newer systemd (see [Versioning](versioning.md#when-a-field-changes-type)). |

<!-- check: var conn *varlink.Conn -->
```go
err := conn.Call(ctx, "io.systemd.Manager.Describe", nil, nil)
var pe *varlink.ProtocolError
if errors.As(err, &pe) {
    log.Printf("bad peer: %s", pe.Msg)
    // conn is closed now; conn.Err() returns the same error.
}
```

## Falling back when a service is missing

On older systemd, or when a service isn't running, its socket doesn't exist.
Generated `Dial` reports that as an error matching `varlink.ErrUnsupported`, so
"use Varlink if available, else D-Bus" is a single check:

```go
func hostnameOf(ctx context.Context) (string, error) {
    c, err := hostname.Dial(ctx)
    if errors.Is(err, varlink.ErrUnsupported) {
        return hostnameViaDBus(ctx) // systemd < v257, or hostnamed not running
    }
    if err != nil {
        return "", err
    }
    defer c.Close()

    h, err := c.Describe(ctx, hostname.DescribeIn{})
    if err != nil {
        return "", err
    }
    return h.Hostname, nil
}

func hostnameViaDBus(ctx context.Context) (string, error) {
    return os.Hostname() // or go-systemd's dbus package
}
```

A method that's missing from an otherwise running service returns
`ErrMethodNotFound`. Rather than calling and catching it, you can
[feature-detect](versioning.md#feature-detection) up front with `Supports`.

## Returning errors from fakes

In tests, return the generated error types (or a `syscall.Errno`) from
`varlinktest` handlers and the client sees exactly what systemd would send.
See [Testing: errors](testing.md#errors).

## See also

- [`varlink` package errors on pkg.go.dev](https://pkg.go.dev/github.com/rossedman/sdvarlink/varlink#pkg-variables)
- Per-interface error tables in the [interface reference](../reference/interfaces.md)
