---
description: Pass a disk image to systemd-mountfsd as a file descriptor and receive mount file descriptors back.
---

# Mount a disk image

:material-github: [Source](https://github.com/rossedman/sdvarlink/tree/main/examples/mount-image) · :material-api: [`io.systemd.MountFileSystem`](../reference/interfaces/mountfilesystem.md) · :material-tag-outline: systemd v257+ · :material-shield-account-outline: Root or polkit

File-descriptor passing in both directions. The program opens a disk image
and hands the **open file** to `systemd-mountfsd`, which dissects it (GPT,
verity, signatures, image policy) and returns one **mount file descriptor**
per partition. No paths cross the socket.

## Run it

```sh
sudo go run ./examples/mount-image app.raw
```

```text title="Output (illustrative)"
image: 536870912 bytes, 512-byte sectors, policy root=verity+signed+encrypted+unprotected+absent:usr=verity+signed+encrypted+unprotected+absent
root       ext4    268435456 bytes  writable=false fd=7
usr        erofs   201326592 bytes  writable=false fd=8
```

## The code

```go title="examples/mount-image/main.go" linenums="1" hl_lines="24 40 51 52"
--8<-- "examples/mount-image/main.go"
```

## How it works

- **Sending.** `ImageFileDescriptor` is an `*os.File` field tagged
  `varlink:"imageFileDescriptor,fd"`. sdvarlink sends it with `SCM_RIGHTS` in
  the same `sendmsg(2)` as the JSON, which carries its index (`0`).
- **Borrowed input.** The image stays open and owned by the program
  (`defer img.Close()`); sdvarlink never closes files you pass in.
- **Owned output.** Each `PartitionInfo.MountFileDescriptor` is a new
  `*os.File` (close-on-exec set) that the caller must close. Attach it with
  `move_mount(2)`, or inspect it via `/proc/self/fd`.
- **Authorization.** mountfsd checks polkit; `AllowInteractiveAuthentication`
  lets it prompt when run from a terminal.

## See also

- [File descriptors guide](../guides/file-descriptors.md)
- [Testing fd fixtures](../guides/testing.md#file-descriptor-fixtures)
