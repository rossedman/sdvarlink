---
description: How sdvarlink-gen maps Varlink IDL types and names to Go types and identifiers.
---

# Type mapping & naming

`sdvarlink-gen` turns each interface's IDL into one Go package. These are the
rules it follows, so you can predict a generated name without looking it up.

## Types

| Varlink IDL | Go | Notes |
| --- | --- | --- |
| `bool` | `bool` | |
| `int` | `int64` | |
| `float` | `float64` | |
| `string` | `string` | |
| `object` | `json.RawMessage` | Arbitrary JSON object; decode it yourself. |
| `any` | `json.RawMessage` | Any JSON value. |
| `?T` | `*T` + `omitempty` | Nullable. Build with `varlink.Ptr`, read with `varlink.Deref`. |
| `?[]T`, `?[string]T`, `?object` | `[]T`, `map[string]T`, `json.RawMessage` + `omitempty` | Already nil-able, so no extra pointer. |
| `[]T` | `[]T` | |
| `[string]T` | `map[string]T` | |
| `Name` (a named type) | `Name` | Struct or enum declared in the same interface. Unknown names fall back to `json.RawMessage`. |
| `(a: T, b: U)` (inline struct) | `<Parent><Field>` struct | Named after where it appears. |
| `(x, y, z)` (inline enum) | `<Parent><Field>` string type + constants | |
| `int` field named `…FileDescriptor` | `*os.File` | Tagged `json:"-" varlink:"<name>,fd"`; sent and received with `SCM_RIGHTS`. See [File descriptors](../guides/file-descriptors.md). |

Every generated struct also gets:

<!-- check:skip -->
```go
// Extra holds fields sent by the service that these bindings do not
// know, such as fields added in newer systemd releases.
Extra map[string]json.RawMessage `json:"-"`
```

plus an `UnmarshalJSON` that fills it. `Extra` is read-only in practice: it's
not encoded when you send a struct.

### Example

=== "IDL"

    ```text
    type ResolvedAddress(
        ifindex: ?int,
        family: int,
        address: []int
    )

    method ResolveHostname(
        ifindex: ?int,
        name: string,
        family: ?int,
        flags: ?int
    ) -> (
        addresses: []ResolvedAddress,
        name: string,
        flags: int
    )
    ```

=== "Generated Go"

    <!-- check:skip -->
    ```go
    type ResolvedAddress struct {
        Ifindex *int64  `json:"ifindex,omitempty"`
        Family  int64   `json:"family"`
        Address []int64 `json:"address"`
        Extra   map[string]json.RawMessage `json:"-"`
    }

    type ResolveHostnameIn struct {
        Ifindex *int64  `json:"ifindex,omitempty"`
        Name    string  `json:"name"`
        Family  *int64  `json:"family,omitempty"`
        Flags   *int64  `json:"flags,omitempty"`
        Extra   map[string]json.RawMessage `json:"-"`
    }

    type ResolveHostnameOut struct {
        Addresses []ResolvedAddress `json:"addresses"`
        Name      string            `json:"name"`
        Flags     int64             `json:"flags"`
        Extra     map[string]json.RawMessage `json:"-"`
    }

    func (c *Client) ResolveHostname(ctx context.Context, in ResolveHostnameIn) (*ResolveHostnameOut, error)
    ```

## Enums

```text
type MachineOpenMode(tty, login, shell)
```

becomes a string type, one constant per value, and `String()`:

<!-- check:skip -->
```go
type MachineOpenMode string

const (
    MachineOpenModeTTY   MachineOpenMode = "tty"
    MachineOpenModeLogin MachineOpenMode = "login"
    MachineOpenModeShell MachineOpenMode = "shell"
)

func (v MachineOpenMode) String() string
```

## Methods

| IDL | Generated on `Client` |
| --- | --- |
| `method X(in...) -> (out...)` | `X(ctx, in XIn) (*XOut, error)` |
| `method X() -> (out...)` | `X(ctx) (*XOut, error)` |
| `method X(in...) -> ()` | `X(ctx, in XIn) error` |
| `method X() -> ()` | `X(ctx) error` |
| `# [Supports 'more' flag]` | `X(...)` as above **and** `XMore(ctx[, in]) iter.Seq2[*XOut, error]` |
| `# [Requires 'more' flag]` | `X(ctx[, in]) iter.Seq2[*XOut, error]` only |

A streaming method with no reply fields yields `*struct{}`.

## Errors

```text
error DNSSECValidationFailed(result: string, extendedDNSErrorCode: ?int, extendedDNSErrorMessage: ?string)
```

becomes:

| Generated | Use |
| --- | --- |
| `type DNSSECValidationFailedError struct{ Result string; ExtendedDNSErrorCode *int64; ... }` | `errors.As(err, &e)` to read parameters |
| `var ErrDNSSECValidationFailed = &varlink.Error{Name: "io.systemd.Resolve.DNSSECValidationFailed"}` | `errors.Is(err, ErrDNSSECValidationFailed)` |
| `func (*DNSSECValidationFailedError) VarlinkError() string` | implements `varlink.NamedError` |

## Names

| Rule | IDL | Go |
| --- | --- | --- |
| Identifiers are exported in CamelCase | `machineId`, `horiz_pre` | `MachineID`, `HorizPre` |
| Common initialisms are upper-cased | `uid`, `pidfdId`, `sshAddress`, `ifindex` | `UID`, `PidfdID`, `SSHAddress`, `Ifindex` |
| Method parameters / reply | `ResolveHostname` | `ResolveHostnameIn` / `ResolveHostnameOut` |
| Error type and sentinel | `error NoSuchUnit` | `NoSuchUnitError` / `ErrNoSuchUnit` |
| Enum constants | `MachineOpenMode` value `tty` | `MachineOpenModeTTY` |
| Inline types | field `limit` of `GetOut` | `GetOutLimit` |
| Methods that clash with `Client`'s own (`Close`, `Caller`, `Supports`) | `method Close()` | `CloseMethod` |
| Any other clash | | trailing `_` (e.g. a field named `Extra` becomes `Extra_`) |

Initialisms recognized: ACL, API, CPU, DNS, EFI, GID, HTTP, ID, IO, IP, JSON,
MAC, MTU, OS, PCR, PID, SSH, TLS, TPM, TTL, TTY, UID, URI, URL, UUID.

!!! note "Error names"

    An IDL error that already ends in `Error` keeps its name:
    `io.systemd.Unit.UnitError` becomes the type `UnitError` with sentinel
    `ErrUnitError`.

!!! note "Corrections to upstream IDL"

    A few systemd releases declare a field with the wrong type in their IDL.
    `idl/generate.json` lists corrections per release under `fieldTypes`,
    and the generator applies them. For example, systemd v258 declares
    `io.systemd.Hostname.Describe`'s `OperatingSystemImageID` and
    `OperatingSystemImageVersion` as `?int`, though hostnamed sends strings
    (v260 fixed the IDL); the v258 bindings use `*string`.

## Packages

| Rule | Interface | Package |
| --- | --- | --- |
| Drop `io.systemd.`, remove dots, lower-case | `io.systemd.Resolve.Monitor` | `resolvemonitor` |
| The bare `io.systemd` interface | `io.systemd` | `systemd` |
| Go keywords get a suffix | `io.systemd.Import` | `importd` (others: `<name>pkg`) |
| Unstable interfaces (configured in `idl/generate.json`) | | `systemd/v<N>/x/<pkg>` |

Each package also exports `InterfaceName`, `SystemdVersion`, `DefaultSocket`
(when known) and `InterfaceDescription`.
