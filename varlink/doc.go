// Package varlink is the protocol core of sdvarlink: a small, dependency-light
// Varlink client for Unix sockets.
//
// Varlink messages are JSON objects terminated by a NUL byte. A client sends
// a call naming a qualified method ("io.systemd.Resolve.ResolveHostname")
// with parameters; the service answers with one reply, a stream of replies
// (when the call sets "more"), an error, or nothing (when the call sets
// "oneway"). File descriptors travel next to the message as SCM_RIGHTS
// ancillary data, and the JSON refers to them by index.
//
// Most programs use the generated packages under systemd/ and reach for
// this package only for custom or unstable methods:
//
//	conn, err := varlink.Dial(ctx, "/run/systemd/io.systemd.Manager")
//	if err != nil {
//		return err
//	}
//	defer conn.Close()
//
//	var out json.RawMessage
//	err = conn.Call(ctx, "io.systemd.Manager.Describe", nil, &out)
//
// A Conn runs one call at a time, as the protocol requires. A Pool hands out
// connections to concurrent callers; generated clients use one by default.
//
// Errors returned by the service arrive as *Error, matched by name with
// errors.Is, or as *SystemError for systemd's errno-carrying
// io.systemd.System error. Generated packages add typed errors per
// interface.
//
// Fields of type *os.File tagged `varlink:"name,fd"` are sent and received
// as file descriptors; see Marshal.
package varlink
