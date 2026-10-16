---
description: The Unix socket where systemd serves each io.systemd.* Varlink interface, and the release that introduced it.
---

# Default sockets

Varlink has no bus and no resolver: each service listens on a fixed socket
path. Generated packages export it as `DefaultSocket`, and `Dial` connects to
it. The table comes from the generator config, `idl/generate.json`.

| Interface | Package | Default socket | Since |
| --- | --- | --- | --- |
| [`io.systemd`](interfaces/systemd.md) | `systemd` | — (`DialPath` only) | v257 |
| [`io.systemd.AskPassword`](interfaces/askpassword.md) | `askpassword` | `/run/systemd/io.systemd.AskPassword` | v258 |
| [`io.systemd.BootControl`](interfaces/bootcontrol.md) | `bootcontrol` | `/run/systemd/io.systemd.BootControl` | v257 |
| [`io.systemd.Credentials`](interfaces/credentials.md) | `credentials` | `/run/systemd/io.systemd.Credentials` | v257 |
| [`io.systemd.FactoryReset`](interfaces/factoryreset.md) | `factoryreset` | `/run/systemd/io.systemd.FactoryReset` | v258 |
| [`io.systemd.Hostname`](interfaces/hostname.md) | `hostname` | `/run/systemd/io.systemd.Hostname` | v257 |
| [`io.systemd.Import`](interfaces/importd.md) | `importd` | `/run/systemd/io.systemd.Import` | v257 |
| [`io.systemd.Journal`](interfaces/journal.md) | `journal` | `/run/systemd/journal/io.systemd.journal` | v257 |
| [`io.systemd.JournalAccess`](interfaces/journalaccess.md) | `journalaccess` | `/run/systemd/io.systemd.JournalAccess` | v260 |
| [`io.systemd.Login`](interfaces/login.md) | `login` | `/run/systemd/io.systemd.Login` | v258 |
| [`io.systemd.Machine`](interfaces/machine.md) | `machine` | `/run/systemd/machine/io.systemd.Machine` | v257 |
| [`io.systemd.MachineImage`](interfaces/machineimage.md) | `machineimage` | `/run/systemd/machine/io.systemd.MachineImage` | v257 |
| [`io.systemd.ManagedOOM`](interfaces/managedoom.md) | `managedoom` | `/run/systemd/io.systemd.ManagedOOM` | v257 |
| [`io.systemd.Manager`](interfaces/manager.md) | `manager` | `/run/systemd/io.systemd.Manager` | v258 |
| [`io.systemd.Metrics`](interfaces/metrics.md) | `metrics` | — (`DialPath` only) | v260 |
| [`io.systemd.MountFileSystem`](interfaces/mountfilesystem.md) | `mountfilesystem` | `/run/systemd/io.systemd.MountFileSystem` | v257 |
| [`io.systemd.MuteConsole`](interfaces/muteconsole.md) | `muteconsole` | `/run/systemd/io.systemd.MuteConsole` | v260 |
| [`io.systemd.NamespaceResource`](interfaces/namespaceresource.md) | `namespaceresource` | `/run/systemd/io.systemd.NamespaceResource` | v257 |
| [`io.systemd.Network`](interfaces/network.md) | `network` | `/run/systemd/netif/io.systemd.Network` | v257 |
| [`io.systemd.Network.Link`](interfaces/networklink.md) | `networklink` | `/run/systemd/netif/io.systemd.Network` | v260 |
| [`io.systemd.oom`](interfaces/oom.md) | `oom` | `/run/systemd/oom/io.systemd.ManagedOOM` | v257 |
| [`io.systemd.oom.Prekill`](interfaces/oomprekill.md) | `oomprekill` | — (`DialPath` only) | v260 |
| [`io.systemd.PCRExtend`](interfaces/pcrextend.md) | `pcrextend` | `/run/systemd/io.systemd.PCRExtend` | v257 |
| [`io.systemd.PCRLock`](interfaces/pcrlock.md) | `pcrlock` | `/run/systemd/io.systemd.PCRLock` | v257 |
| [`io.systemd.Repart`](interfaces/repart.md) | `repart` | `/run/systemd/io.systemd.Repart` | v260 |
| [`io.systemd.Resolve`](interfaces/resolve.md) | `resolve` | `/run/systemd/resolve/io.systemd.Resolve` | v257 |
| [`io.systemd.Resolve.Hook`](interfaces/resolvehook.md) | `resolvehook` | — (`DialPath` only) | v260 |
| [`io.systemd.Resolve.Monitor`](interfaces/resolvemonitor.md) | `resolvemonitor` | `/run/systemd/resolve/io.systemd.Resolve.Monitor` | v257 |
| [`io.systemd.service`](interfaces/service.md) | `service` | — (`DialPath` only) | v257 |
| [`io.systemd.sysext`](interfaces/sysext.md) | `sysext` | `/run/systemd/io.systemd.sysext` | v257 |
| [`io.systemd.Udev`](interfaces/udev.md) | `udev` | `/run/udev/io.systemd.Udev` | v258 |
| [`io.systemd.Unit`](interfaces/unit.md) | `unit` | `/run/systemd/io.systemd.Manager` | v258 |
| [`io.systemd.UserDatabase`](interfaces/userdatabase.md) | `userdatabase` | `/run/systemd/userdb/io.systemd.Multiplexer` | v257 |

## Notes

- **Shared sockets.** One socket can serve several interfaces. PID 1 serves
  `io.systemd.Manager` and `io.systemd.Unit` on `/run/systemd/io.systemd.Manager`;
  `systemd-networkd` serves `io.systemd.Network` and `io.systemd.Network.Link` on
  `/run/systemd/netif/io.systemd.Network`. Share a pool between their clients
  with `New`; see [Concurrency](../guides/concurrency.md#tuning-or-sharing-the-pool).
- **Every socket also serves** `org.varlink.service` (introspection) and usually
  `io.systemd` (generic errors) and `io.systemd.service` (`Ping`, `SetLogLevel`,
  `GetEnvironment`, ...).
- **No default socket** means the interface is implemented by several daemons
  or on dynamic paths: `io.systemd.Metrics` sockets live under
  `/run/systemd/report/`; `io.systemd.Resolve.Hook` and `io.systemd.oom.Prekill`
  are implemented by *clients* that systemd calls. Use `DialPath`.
- **"Since"** is the oldest release among those sdvarlink generates bindings for
  (v257, v258, v260) whose IDL contains the interface. Some interfaces existed
  before v257.
- **User services.** Per-user instances (e.g. `systemd --user`) serve sockets
  under `$XDG_RUNTIME_DIR/systemd/`. Connect with `DialPath`.
- **Containers.** A container's sockets are reachable from the host under its
  root directory, e.g. `/var/lib/machines/<name>/run/systemd/...`, subject to
  permissions.

## Discovering sockets on a host

```sh
find /run/systemd /run/udev -type s -name 'io.systemd*' 2>/dev/null
```

```go
socks, _ := filepath.Glob("/run/systemd/report/*") // every metrics provider
for _, s := range socks {
    fmt.Println(s)
}
```
