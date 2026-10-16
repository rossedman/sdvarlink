---
description: Map of sdvarlink's Go packages and their main APIs, with links to pkg.go.dev.
---

# API reference

sdvarlink's Go API is documented on
[pkg.go.dev](https://pkg.go.dev/github.com/rossedman/sdvarlink). This page is a
map: what each package is for, and its main types and functions at a glance.

<div class="grid cards" markdown>

-   :material-api:{ .lg .middle } **[Interfaces](interfaces.md)**

    ---

    Every `io.systemd.*` interface: methods, types, errors, and the release
    that introduced each. Generated from systemd's IDL.

-   :material-compare-horizontal:{ .lg .middle } **[API changes](api-changes.md)**

    ---

    What systemd added, changed and removed between the releases sdvarlink
    covers.

-   :material-swap-vertical:{ .lg .middle } **[Type mapping & naming](type-mapping.md)**

    ---

    How IDL types and names become Go types and identifiers.

-   :material-console-line:{ .lg .middle } **[sdvarlink-gen](cli.md)**

    ---

    The generator CLI: `generate`, `diff`, `docs`.

</div>

## Packages

| Package | Purpose | pkg.go.dev |
| --- | --- | --- |
| `varlink` | Protocol core: connections, pools, raw calls, streams, fd passing, errors, introspection. | [:material-open-in-new:](https://pkg.go.dev/github.com/rossedman/sdvarlink/varlink) |
| `varlinktest` | Fake Varlink server for unit tests; record/replay. | [:material-open-in-new:](https://pkg.go.dev/github.com/rossedman/sdvarlink/varlinktest) |
| `idl` | Varlink IDL parser. | [:material-open-in-new:](https://pkg.go.dev/github.com/rossedman/sdvarlink/idl) |
| `systemd/latest/<pkg>` | Typed clients for the newest systemd release (aliases of `v260`). | [:material-open-in-new:](https://pkg.go.dev/github.com/rossedman/sdvarlink/systemd/latest/resolve) |
| `systemd/v<N>/<pkg>` | Typed clients pinned to systemd release *N* (257, 258, 260). | [:material-open-in-new:](https://pkg.go.dev/github.com/rossedman/sdvarlink/systemd/v257/resolve) |
| `cmd/sdvarlink-gen` | The generator. | [:material-open-in-new:](https://pkg.go.dev/github.com/rossedman/sdvarlink/cmd/sdvarlink-gen) |

## `varlink`

### Connections

| API | Description |
| --- | --- |
| `Dial(ctx, path, opts...) (*Conn, error)` | Connect to the Varlink service at a Unix socket path. |
| `DialService(ctx, path, opts...) (*Conn, error)` | `Dial` for systemd sockets: a missing socket (or non-Linux) matches `ErrUnsupported`. |
| `NewConn(*net.UnixConn, opts...) *Conn` | Wrap an existing connection (socketpair, socket activation). |
| `WithMaxFrameSize(n) Option` | Largest message accepted (default `DefaultMaxFrameSize`, 16 MiB). |
| `WithMaxFiles(n) Option` | Most fds accepted per read and sent per message (default `DefaultMaxFiles`, 16). |
| `(*Conn).Close() error` / `Err() error` | Close; report why a connection became unusable. |

### Calls

| API | Description |
| --- | --- |
| `Caller` | Interface implemented by `*Conn` and `*Pool`: `Call`, `Stream`, `Oneway`. Generated clients are built on it. |
| `Call(ctx, method, in, out, opts...) error` | One call, one reply decoded into `out`. |
| `CallReply(ctx, method, in, opts...) (*Reply, error)` | One call, raw reply including fds. (`*Conn` only.) |
| `Stream(ctx, method, in, opts...) iter.Seq2[*Reply, error]` | Call with `more`; yields each reply. Breaking early closes the connection. |
| `Oneway(ctx, method, in, opts...) error` | Call with `oneway`; no reply. |
| `StreamAs[T](seq) iter.Seq2[*T, error]` | Decode each streamed reply into a `T`. |
| `WithFiles(files...) CallOption` | Send extra fds; indexes follow the fd fields. |
| `Reply` | `Parameters`, `Files`, `Continues`; `Decode(v)`, `Close()`. |

### Pools

| API | Description |
| --- | --- |
| `NewPool(dial DialFunc, opts...) *Pool` | Pool of connections from your dial function. |
| `NewPoolPath(path, opts...) *Pool` | Pool dialing a socket path. |
| `WithMaxIdle(n) PoolOption` | Idle connections kept (default `DefaultMaxIdle`, 4). |
| `(*Pool).Get(ctx)` / `Put(c)` / `Close()` | Borrow, return, shut down. Broken connections are discarded. |

### Codec

| API | Description |
| --- | --- |
| `Marshal(v) ([]byte, []*os.File, error)` | Encode parameters; collect `varlink:"name,fd"` files and write their indexes. |
| `Unmarshal(data, files, v) error` | Decode and attach files to fd fields; close unclaimed files. |
| `UnknownFields(data, known...)` | Members of a JSON object not in `known` (fills generated `Extra` maps). |
| `Ptr(v) *T` / `Deref(p) T` | Optional-field helpers. |

### Errors

| API | Description |
| --- | --- |
| `Error` | Error reply: `Name`, `Parameters`; `Is` matches by name; `Decode(v)`. |
| `SystemError` | `io.systemd.System`: `Errno`, `ErrnoName`, `Origin`; unwraps to `syscall.Errno`. |
| `NamedError` / `ErrorName(err)` | Read the Varlink error name of any error. |
| `ProtocolError` | Malformed peer message; connection closed. |
| `ErrUnsupported`, `ErrClosed`, `ErrFrameTooLarge`, `ErrTooManyFiles` | Transport sentinels. |
| `ErrInterfaceNotFound`, `ErrMethodNotFound`, `ErrMethodNotImplemented`, `ErrInvalidParameter`, `ErrPermissionDenied`, `ErrExpectedMore` | `org.varlink.service` error sentinels. |

### Introspection

| API | Description |
| --- | --- |
| `GetInfo(ctx, caller) (*Info, error)` | Vendor, product, version, URL, interfaces. |
| `GetInterfaceDescription(ctx, caller, iface) (string, error)` | IDL text of one interface. |
| `Supports(ctx, caller, "iface.Method") (bool, error)` | Whether the live service has the method. |
| `SplitMethod("iface.Method")` | Split a qualified name. |

## Generated packages

Every package under `systemd/` exposes the same shape:

| API | Description |
| --- | --- |
| `InterfaceName`, `SystemdVersion`, `DefaultSocket` | Constants. `DefaultSocket` exists when the interface has a well-known socket. |
| `InterfaceDescription` | The embedded IDL. |
| `Dial(ctx, opts...)` | Connect to `DefaultSocket` (pooled). |
| `DialPath(ctx, path, opts...)` | Connect to any socket (pooled). |
| `New(caller)` | Client over any `varlink.Caller`. |
| `(*Client).Close()`, `Caller()`, `Supports(ctx, method)` | Lifecycle, raw access, feature detection. |
| `(*Client).<Method>` / `<Method>More` | One per IDL method; see [streaming](../guides/streaming.md#which-methods-stream). |
| `<Method>In`, `<Method>Out`, types, enums | Generated from the IDL; see [type mapping](type-mapping.md). |
| `<Name>Error`, `Err<Name>` | Typed errors and sentinels. |

## `varlinktest`

| API | Description |
| --- | --- |
| `NewServer(tb) *Server` | Fake service on a temp socket; closed at test end. |
| `(*Server).Path()` / `Dial(opts...)` | Socket path; a `*varlink.Conn` closed at test end. |
| `(*Server).Handle(method, fn)` | Typed handler via reflection: `func(In) (Out, error)`, `func(ctx, In) (Out, error)`, `func(ctx, In) iter.Seq2[Out, error]`. |
| `On[In, Out](srv, method, fn)` / `OnStream[In, Out](...)` | Compile-time-checked typed handlers. |
| `(*Server).HandleRaw(method, RawHandler)` | Raw handler over `*Call` → `*Response`s. |
| `(*Server).AddInterface(idl)` | Serve an interface description for introspection and `Supports`. |
| `(*Server).Calls() []Call` | Calls received, in order. |
| `Record(tb, upstream, golden)` / `Replay(tb, golden)` | Golden-file record/replay against real services. |
| `HandlerError` | Error name sent for plain Go errors from handlers. |

## `idl`

| API | Description |
| --- | --- |
| `Parse(src) (*Interface, error)` | Parse one interface's IDL. |
| `Interface` | `Name`, `Doc`, `Types`, `Methods`, `Errors`; `Type()`, `Method()`, `Error()` lookups. |
| `Method` | `Name`, `Doc`, `In`, `Out`, `More` (`MoreNone`, `MoreSupported`, `MoreRequired`). |
| `Type`, `Field`, `TypeDef`, `EnumValue`, `Error` | The type tree; `Type.String()` prints IDL syntax. |
| `SyntaxError` | `Line`, `Col`, `Msg`. |
