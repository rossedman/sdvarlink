package varlink_test

import (
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/rossedman/sdvarlink/varlink"
)

// TestServerProtocolErrors sends calls the server must refuse with a typed
// error, each followed by an Echo that must still be answered: these errors
// keep the connection open.
func TestServerProtocolErrors(t *testing.T) {
	t.Parallel()
	// NoName and BadParams stay unimplemented.
	path := serveR(t, func(iface *varlink.Interface) {
		echoHandler(iface)
		varlink.HandleStream(iface, "Leaky", func(ctx context.Context, call *varlink.Call, _ struct{}, send func(nOut) error) error {
			return send(nOut{N: 1})
		})
	})
	const after = `{"method":"com.example.R.Echo","parameters":{"text":"after"}}` + "\x00"
	tests := []struct {
		name string
		call string
		want string // substring of the first reply
	}{
		{name: "upgrade", call: `{"method":"com.example.R.Echo","upgrade":true}`, want: `"error":"org.varlink.service.MethodNotImplemented"`},
		{name: "method without interface", call: `{"method":"Echo"}`, want: `"error":"org.varlink.service.MethodNotFound"`},
		{name: "unknown interface", call: `{"method":"com.example.Nope.Echo"}`, want: `"error":"org.varlink.service.InterfaceNotFound"`},
		{name: "method not in the IDL", call: `{"method":"com.example.R.Missing"}`, want: `"error":"org.varlink.service.MethodNotFound"`},
		{name: "method in the IDL without a handler", call: `{"method":"com.example.R.NoName"}`, want: `"error":"org.varlink.service.MethodNotImplemented"`},
		{name: "requires more, called without", call: `{"method":"com.example.R.Leaky"}`, want: `"error":"org.varlink.service.ExpectedMore"`},
		{name: "invalid parameter", call: `{"method":"com.example.R.Echo","parameters":{"text":7}}`, want: `"error":"org.varlink.service.InvalidParameter"`},
		{name: "unknown builtin", call: `{"method":"org.varlink.service.Nope"}`, want: `"error":"org.varlink.service.MethodNotFound"`},
		{name: "description without interface", call: `{"method":"org.varlink.service.GetInterfaceDescription","parameters":{}}`, want: `"error":"org.varlink.service.InvalidParameter"`},
		{name: "description of an unknown interface", call: `{"method":"org.varlink.service.GetInterfaceDescription","parameters":{"interface":"com.example.Nope"}}`, want: `"error":"org.varlink.service.InterfaceNotFound"`},
		{name: "description of the service interface", call: `{"method":"org.varlink.service.GetInterfaceDescription","parameters":{"interface":"org.varlink.service"}}`, want: `interface org.varlink.service`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			replies := rawExchange(t, path, tc.call+"\x00"+after, 2, 20*time.Millisecond, false)
			if !strings.Contains(replies[0], tc.want) {
				t.Errorf("reply = %s, want containing %s", replies[0], tc.want)
			}
			if !strings.Contains(replies[1], `"text":"after"`) {
				t.Errorf("connection not usable after the error: %s", replies[1])
			}
		})
	}
}

// TestServerMalformedCallCloses checks that messages that are not calls get
// no reply and close the connection, as sd-varlink does.
func TestServerMalformedCallCloses(t *testing.T) {
	t.Parallel()
	path := serveR(t, echoHandler)
	tests := []struct {
		name string
		msg  string
	}{
		{name: "not JSON", msg: `{"method":`},
		{name: "no method", msg: `{"parameters":{}}`},
		{name: "empty method", msg: `{"method":""}`},
		{name: "parameters not an object", msg: `{"method":"com.example.R.Echo","parameters":["x"]}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c, err := net.Dial("unix", path)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if _, err := c.Write([]byte(tc.msg + "\x00")); err != nil {
				t.Fatal(err)
			}
			c.SetReadDeadline(time.Now().Add(5 * time.Second))
			got, err := io.ReadAll(c)
			if err != nil {
				t.Fatalf("read: %v (want the server to close the connection)", err)
			}
			if len(got) != 0 {
				t.Errorf("got reply %q, want none", got)
			}
		})
	}
}

// TestServerParametersNullIsEmpty checks that "parameters": null is the
// same as leaving them out.
func TestServerParametersNullIsEmpty(t *testing.T) {
	t.Parallel()
	path := serveR(t, func(iface *varlink.Interface) {})
	replies := rawExchange(t, path, `{"method":"org.varlink.service.GetInfo","parameters":null}`+"\x00", 1, 20*time.Millisecond, false)
	if !strings.Contains(replies[0], `"interfaces":["com.example.R","org.varlink.service"]`) {
		t.Errorf("GetInfo = %s", replies[0])
	}
}
