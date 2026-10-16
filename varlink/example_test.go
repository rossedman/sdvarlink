package varlink_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"syscall"

	"github.com/rossedman/sdvarlink/varlink"
)

func Example() {
	ctx := context.Background()
	conn, err := varlink.Dial(ctx, "/run/systemd/io.systemd.Manager")
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	var out json.RawMessage
	if err := conn.Call(ctx, "io.systemd.Manager.Describe", nil, &out); err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(out))
}

func ExampleConn_Stream() {
	ctx := context.Background()
	conn, err := varlink.Dial(ctx, "/run/systemd/userdb/io.systemd.Multiplexer")
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	type userRecord struct {
		Record struct {
			UserName string `json:"userName"`
			UID      int    `json:"uid"`
		} `json:"record"`
	}
	in := map[string]string{"service": "io.systemd.Multiplexer"}
	for u, err := range varlink.StreamAs[userRecord](conn.Stream(ctx, "io.systemd.UserDatabase.GetUserRecord", in)) {
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(u.Record.UserName, u.Record.UID)
	}
}

func ExampleMarshal_fileDescriptors() {
	// Fields of type *os.File tagged `varlink:"name,fd"` travel as file
	// descriptors; the JSON carries their index.
	type mountImageIn struct {
		Image    *os.File `json:"-" varlink:"imageFileDescriptor,fd"`
		ReadOnly bool     `json:"readOnly"`
	}
	img, _ := os.Open(os.DevNull)
	defer img.Close()

	params, files, err := varlink.Marshal(mountImageIn{Image: img, ReadOnly: true})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(params), len(files))
	// Output: {"imageFileDescriptor":0,"readOnly":true} 1
}

func ExampleSystemError() {
	// systemd reports errno failures as io.systemd.System, which unwraps to
	// syscall.Errno.
	var err error = &varlink.SystemError{Origin: "linux", ErrnoName: "ENOENT", Errno: int(syscall.ENOENT)}
	fmt.Println(errors.Is(err, syscall.ENOENT))
	// Output: true
}

func ExampleError_Is() {
	err := error(&varlink.Error{Name: "org.varlink.service.MethodNotFound"})
	fmt.Println(errors.Is(err, varlink.ErrMethodNotFound), varlink.ErrorName(err))
	// Output: true org.varlink.service.MethodNotFound
}

func ExampleSupports() {
	ctx := context.Background()
	pool := varlink.NewPoolPath("/run/systemd/io.systemd.Manager")
	defer pool.Close()

	ok, err := varlink.Supports(ctx, pool, "io.systemd.Unit.List")
	if err != nil {
		log.Fatal(err)
	}
	if !ok {
		fmt.Println("systemd is older than v258: list units over D-Bus instead")
	}
}
