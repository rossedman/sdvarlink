---
description: Use sdvarlink's core varlink package directly — raw JSON calls, CallReply, streams, oneway, WithFiles, introspection and the IDL parser.
---

# Raw calls & the core package

The generated packages are built on a small core, `varlink`, which you can use
directly for methods the bindings don't cover: experimental interfaces, your
own Varlink services, fields from a systemd newer than your bindings, or quick
scripts where typing everything isn't worth it. It's `varlinkctl call`, in Go.

## Dial and call

```go
conn, err := varlink.Dial(ctx, "/run/systemd/io.systemd.Hostname")
if err != nil {
    return err
}
defer conn.Close()

var out json.RawMessage
if err := conn.Call(ctx, "io.systemd.Hostname.Describe", nil, &out); err != nil {
    return err
}
fmt.Println(string(out))
```

`Call(ctx, method, in, out, opts...)`:

- `method` is the fully qualified name, `<interface>.<method>`.
- `in` is anything that encodes to a JSON object: a struct, a
  `map[string]any`, a `json.RawMessage`, or `nil` for `{}`.
- `out` is a pointer to decode into: a struct, a map, a `*json.RawMessage`,
  or `nil` to discard the reply.

Your own structs work as well as `json.RawMessage`:

```go
type describeOut struct {
    Hostname      string `json:"Hostname"`
    KernelRelease string `json:"KernelRelease"`
    MachineID     string `json:"MachineID"`
}

func describe(ctx context.Context, conn *varlink.Conn) (*describeOut, error) {
    var out describeOut
    err := conn.Call(ctx, "io.systemd.Hostname.Describe", nil, &out)
    return &out, err
}
```

`Dial` returns the raw dial error when the socket is missing. For systemd
sockets, `varlink.DialService` wraps "no such file" and "connection refused" in
`varlink.ErrUnsupported`, as generated clients do.

## Streams and replies

`Stream` sets `"more"` and yields each `*varlink.Reply`; `StreamAs[T]` decodes
them for you:

=== "Stream"

    <!-- check: var conn *varlink.Conn -->
    ```go
    in := json.RawMessage(`{"service":"io.systemd.Multiplexer"}`)
    for r, err := range conn.Stream(ctx, "io.systemd.UserDatabase.GetUserRecord", in) {
        if err != nil {
            return err
        }
        fmt.Println(string(r.Parameters))
        r.Close() // release any fds this reply carried
    }
    ```

=== "StreamAs"

    ```go
    type userReply struct {
        Record     json.RawMessage `json:"record"`
        Incomplete bool            `json:"incomplete"`
    }

    func listUsers(ctx context.Context, conn *varlink.Conn) error {
        in := map[string]string{"service": "io.systemd.Multiplexer"}
        seq := conn.Stream(ctx, "io.systemd.UserDatabase.GetUserRecord", in)
        for u, err := range varlink.StreamAs[userReply](seq) {
            if err != nil {
                return err
            }
            fmt.Println(string(u.Record))
        }
        return nil
    }
    ```

A `*varlink.Reply` is the raw form of one reply:

| Field / method | Meaning |
| --- | --- |
| `Parameters json.RawMessage` | The reply's parameters. |
| `Files []*os.File` | Descriptors that arrived with this reply. |
| `Continues bool` | More replies follow. |
| `Decode(v any) error` | Unmarshal into `v` and hand fds to its fd fields; unclaimed fds are closed. Call once. |
| `Close()` | Close any fds `Decode` didn't hand out. |

`CallReply` is `Call` without decoding: it returns the single `*Reply`, which
is how you keep descriptors from a raw call.

<!-- check: var conn *varlink.Conn; var img *os.File -->
```go
r, err := conn.CallReply(ctx, "io.systemd.MountFileSystem.MountImage",
    json.RawMessage(`{"imageFileDescriptor":0,"readOnly":true}`), varlink.WithFiles(img))
if err != nil {
    return err
}
// r.Parameters: {"partitions":[{"mountFileDescriptor":0,...}],...}
// r.Files:      [*os.File for each mount fd], now yours
for _, f := range r.Files {
    defer f.Close()
}
```

## Oneway

`Oneway` sends a call with `"oneway": true` and returns as soon as it's
written. The service sends nothing back, not even errors:

<!-- check: var conn *varlink.Conn -->
```go
// Ask journald to sync, without waiting.
if err := conn.Oneway(ctx, "io.systemd.Journal.Synchronize", nil); err != nil {
    return err // only write errors
}
```

## Passing file descriptors

Tag `*os.File` fields with `varlink:"<name>,fd"` and the codec sends them as
`SCM_RIGHTS` and writes their indexes into the JSON. Or append files
positionally with `varlink.WithFiles`, whose indexes start after any tagged
fields (at `0` for raw JSON). See [File descriptors](file-descriptors.md#raw-calls-with-descriptors).

The codec is exported too, if you build your own transport or tests:

```go
type in struct {
    Image *os.File `json:"-" varlink:"imageFileDescriptor,fd"`
    Where string   `json:"where"`
}

func encode(img *os.File) error {
    params, files, err := varlink.Marshal(in{Image: img, Where: "/mnt"})
    if err != nil {
        return err
    }
    fmt.Println(string(params), len(files)) // {"imageFileDescriptor":0,"where":"/mnt"} 1
    return nil
}
```

## Introspection

Every Varlink service answers `org.varlink.service.GetInfo` and
`GetInterfaceDescription`. The core wraps both, and the `idl` package parses
what comes back:

<!-- check: var conn *varlink.Conn -->
```go
info, err := varlink.GetInfo(ctx, conn)
if err != nil {
    return err
}
fmt.Println(info.Vendor, info.Product, info.Version)

for _, name := range info.Interfaces {
    desc, err := varlink.GetInterfaceDescription(ctx, conn, name)
    if err != nil {
        return err
    }
    iface, err := idl.Parse(desc)
    if err != nil {
        return err
    }
    for _, m := range iface.Methods {
        fmt.Printf("%s.%s%s -> %s  [%s]\n", iface.Name, m.Name, m.In, m.Out, m.More)
    }
}
```

Against `systemd-journald` v255 that prints:

```text
The systemd Project systemd (systemd-journald) 255 (255.4-1ubuntu8.17)
io.systemd.Journal.Synchronize() -> ()  [single]
io.systemd.Journal.Rotate() -> ()  [single]
io.systemd.Journal.FlushToVar() -> ()  [single]
io.systemd.Journal.RelinquishVar() -> ()  [single]
org.varlink.service.GetInfo() -> (vendor: string, product: string, version: string, url: string, interfaces: []string)  [single]
org.varlink.service.GetInterfaceDescription(interface: string) -> (description: string)  [single]
```

`varlink.Supports(ctx, caller, "iface.Method")` is built on the same two
calls; see [feature detection](versioning.md#feature-detection).

## The IDL parser

`idl.Parse` understands the IDL systemd emits, including doc comments and
systemd's `# [Requires 'more' flag]` / `# [Supports 'more' flag]` annotations:

```go
iface, err := idl.Parse(resolve.InterfaceDescription)
if err != nil {
    return err
}
fmt.Println(iface.Name, len(iface.Methods), "methods")

m := iface.Method("ResolveHostname")
fmt.Println(m.Doc) // "Resolves a hostname to one or more IP addresses."
for _, f := range m.In.Fields {
    fmt.Printf("  %-8s %s\n", f.Name, f.Type) // "  name     string", "  family   ?int", ...
}

if t := iface.Type("ResolvedAddress"); t != nil {
    fmt.Println(t.Name, t.Type) // ResolvedAddress (ifindex: ?int, family: int, address: []int)
}
for _, e := range iface.Errors {
    fmt.Println("error", e.Name, e.Params)
}
```

| Type | Holds |
| --- | --- |
| `idl.Interface` | `Name`, `Doc`, `Types`, `Methods`, `Errors`; lookups `Type(name)`, `Method(name)`, `Error(name)`. |
| `idl.Method` | `Name`, `Doc`, `In`, `Out` (struct types) and `More`: `idl.MoreNone`, `idl.MoreSupported` or `idl.MoreRequired`. |
| `idl.Type` | `Kind` (`Bool`, `Int`, `Float`, `String`, `Object`, `Any`, `Named`, `Struct`, `Enum`, `Array`, `Map`, `Nullable`), `Elem`, `Name`, `Fields`, `Values`; `String()` prints IDL syntax. |
| `idl.SyntaxError` | `Line`, `Col`, `Msg` for malformed input. |

## Helpers

| Function | Purpose |
| --- | --- |
| `varlink.Ptr(v)` | Pointer to `v`, for optional input fields. |
| `varlink.Deref(p)` | `*p`, or the zero value for `nil`. |
| `varlink.SplitMethod("a.b.C")` | `"a.b", "C"`, or an error if not qualified. |
| `varlink.UnknownFields(data, known...)` | The members of a JSON object not in `known`; how generated types fill `Extra`. |
| `varlink.ErrorName(err)` | The Varlink error name carried by `err`, or `""`. |

## A minimal `varlinkctl call`

The [raw-call example](../examples/raw-call.md) is a 70-line `varlinkctl call`
clone, with `-more` and `-oneway`:

```sh
go run ./examples/raw-call /run/systemd/journal/io.systemd.journal org.varlink.service.GetInfo
```

```json
{
  "vendor": "The systemd Project",
  "product": "systemd (systemd-journald)",
  "version": "255 (255.4-1ubuntu8.17)",
  "url": "https://systemd.io/",
  "interfaces": [
    "io.systemd",
    "io.systemd.Journal",
    "org.varlink.service"
  ]
}
```

## See also

- [`varlink` on pkg.go.dev](https://pkg.go.dev/github.com/rossedman/sdvarlink/varlink),
  [`idl` on pkg.go.dev](https://pkg.go.dev/github.com/rossedman/sdvarlink/idl)
- Examples: [Introspect a service](../examples/introspect.md), [Raw calls](../examples/raw-call.md)
