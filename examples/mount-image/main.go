// Command mount-image asks systemd-mountfsd to dissect a disk image and
// return mount file descriptors for its partitions. The image goes to
// systemd as a file descriptor, and the mounts come back as file
// descriptors: no paths cross the socket. Requires root (or polkit
// permission) and systemd-mountfsd.
//
//	sudo go run ./examples/mount-image app.raw
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/rossedman/sdvarlink/systemd/v257/mountfilesystem"
	"github.com/rossedman/sdvarlink/varlink"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("usage: mount-image IMAGE")
	}
	img, err := os.Open(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	defer img.Close()

	ctx := context.Background()
	c, err := mountfilesystem.Dial(ctx)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	// The *os.File is sent with SCM_RIGHTS; the JSON carries its index.
	// sdvarlink never closes files you pass in.
	out, err := c.MountImage(ctx, mountfilesystem.MountImageIn{
		ImageFileDescriptor:            img,
		ReadOnly:                       varlink.Ptr(true),
		AllowInteractiveAuthentication: varlink.Ptr(true),
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("image: %d bytes, %d-byte sectors, policy %s\n", out.ImageSize, out.SectorSize, out.ImagePolicy)
	for _, p := range out.Partitions {
		// Returned files belong to the caller. A mount fd can be attached
		// with move_mount(2), or inspected through /proc/self/fd.
		fmt.Printf("%-10s %-6s %10d bytes  writable=%-5v fd=%d\n", p.Designator, p.FileSystemType, p.Size, p.Writable, p.MountFileDescriptor.Fd())
		p.MountFileDescriptor.Close()
	}
}
