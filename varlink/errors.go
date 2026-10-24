package varlink

import (
	"encoding/json"
	"errors"
	"fmt"
	"syscall"

	"github.com/rossedman/sdvarlink/internal/wire"
)

var (
	// ErrUnsupported is returned when the requested service cannot be used on
	// this host: the platform is not Linux, or the service's socket does not
	// exist (systemd is older than the interface, or the service is not
	// running). Callers typically fall back to D-Bus when they see it.
	ErrUnsupported = wire.ErrUnsupported

	// ErrClosed is returned when a call is made on a closed connection or pool.
	ErrClosed = errors.New("varlink: connection closed")

	// ErrFrameTooLarge is returned when a peer sends a message larger than
	// the configured frame limit (see WithMaxFrameSize).
	ErrFrameTooLarge = wire.ErrFrameTooLarge

	// ErrTooManyFiles is returned when a peer passes more file descriptors
	// than the configured limit (see WithMaxFiles), or the kernel truncated
	// the ancillary data.
	ErrTooManyFiles = wire.ErrTooManyFiles
)

// Well-known errors defined by the org.varlink.service interface. Match them
// with errors.Is; any *Error with the same name matches.
var (
	ErrInterfaceNotFound    = &Error{Name: "org.varlink.service.InterfaceNotFound"}
	ErrMethodNotFound       = &Error{Name: "org.varlink.service.MethodNotFound"}
	ErrMethodNotImplemented = &Error{Name: "org.varlink.service.MethodNotImplemented"}
	ErrInvalidParameter     = &Error{Name: "org.varlink.service.InvalidParameter"}
	ErrPermissionDenied     = &Error{Name: "org.varlink.service.PermissionDenied"}
	ErrExpectedMore         = &Error{Name: "org.varlink.service.ExpectedMore"}
)

// Error is a Varlink error reply: a fully qualified error name plus optional
// parameters. Generated packages convert known names into typed errors; any
// other name arrives as *Error.
type Error struct {
	// Name is the qualified error name, e.g. "io.systemd.Resolve.NoNameServers".
	Name string
	// Parameters holds the raw error parameters, or nil.
	Parameters json.RawMessage
}

// Error returns the error name, followed by its parameters when there are any.
func (e *Error) Error() string {
	if len(e.Parameters) == 0 || string(e.Parameters) == "{}" || string(e.Parameters) == "null" {
		return "varlink: " + e.Name
	}
	return fmt.Sprintf("varlink: %s %s", e.Name, e.Parameters)
}

// Is reports whether target is an *Error with the same name, so that
// errors.Is(err, varlink.ErrMethodNotFound) works for any reply carrying
// that name.
func (e *Error) Is(target error) bool {
	var t *Error
	return errors.As(target, &t) && t.Name == e.Name
}

// VarlinkError returns the qualified error name. Generated error types
// implement the same method, so callers can read the name of any Varlink
// error through the NamedError interface.
func (e *Error) VarlinkError() string { return e.Name }

// Decode unmarshals the error parameters into v.
func (e *Error) Decode(v any) error {
	if len(e.Parameters) == 0 {
		return nil
	}
	return json.Unmarshal(e.Parameters, v)
}

// NamedError is implemented by *Error and by every generated error type.
type NamedError interface {
	error
	VarlinkError() string
}

// ErrorName returns the Varlink error name carried by err, or "" if err is
// not a Varlink error.
func ErrorName(err error) string {
	var n NamedError
	if errors.As(err, &n) {
		return n.VarlinkError()
	}
	return ""
}

// SystemError is systemd's generic io.systemd.System error, which carries a
// Linux errno. It unwraps to syscall.Errno, so errors.Is(err, syscall.ENOENT)
// works.
type SystemError struct {
	Origin    string `json:"origin,omitempty"`
	ErrnoName string `json:"errnoName,omitempty"`
	Errno     int    `json:"errno,omitempty"`
}

// Error describes the errno, using its name when one was sent.
func (e *SystemError) Error() string {
	name := e.ErrnoName
	if name == "" {
		name = fmt.Sprintf("errno %d", e.Errno)
	}
	if e.Errno != 0 {
		return fmt.Sprintf("varlink: io.systemd.System: %s: %v", name, syscall.Errno(e.Errno))
	}
	return "varlink: io.systemd.System: " + name
}

// VarlinkError implements NamedError.
func (e *SystemError) VarlinkError() string { return "io.systemd.System" }

// Unwrap returns the errno as a syscall.Errno, or nil if none was sent.
func (e *SystemError) Unwrap() error {
	if e.Errno == 0 {
		return nil
	}
	return syscall.Errno(e.Errno)
}

// replyError builds the error for a reply that carries an error name.
func replyError(name string, params json.RawMessage) error {
	if name == "io.systemd.System" {
		se := new(SystemError)
		if len(params) > 0 && json.Unmarshal(params, se) == nil {
			return se
		}
	}
	return &Error{Name: name, Parameters: params}
}

// ProtocolError reports a malformed message from the peer. The connection is
// closed after one is returned.
type ProtocolError struct {
	Msg string
	Err error
}

// Error returns the message, followed by the underlying error when there is
// one.
func (e *ProtocolError) Error() string {
	if e.Err != nil {
		return "varlink: protocol error: " + e.Msg + ": " + e.Err.Error()
	}
	return "varlink: protocol error: " + e.Msg
}

// Unwrap returns the underlying error, or nil.
func (e *ProtocolError) Unwrap() error { return e.Err }
