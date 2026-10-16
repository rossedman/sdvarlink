---
description: Report which Varlink services and methods the host's systemd offers, with ErrUnsupported and Supports.
---

# Feature detection

:material-github: [Source](https://github.com/rossedman/sdvarlink/tree/main/examples/feature-detect) · :material-tag-outline: systemd v257+ · :material-shield-account-outline: Unprivileged

A support matrix for the host's systemd, built from the two checks every
program that spans systemd releases needs: **is the service there?**
(`Dial` → `varlink.ErrUnsupported`) and **does it have this method?**
(`Client.Supports`).

## Run it

```sh
go run ./examples/feature-detect
```

```text title="Output (real, a systemd v255 host: below the Varlink floor)"
FEATURE                          METHOD                STATUS
Describe the service manager     Describe              ✗ service not available (fall back to D-Bus)
List units over Varlink (v258+)  List                  ✗ service not available (fall back to D-Bus)
Change unit properties (v260+)   SetProperties         ✗ service not available (fall back to D-Bus)
Resolve host names               ResolveHostname       ✗ service not available (fall back to D-Bus)
Browse DNS-SD services           BrowseServices        ✗ service not available (fall back to D-Bus)
Dump DNS configuration           DumpDNSConfiguration  ✗ service not available (fall back to D-Bus)
Describe the host                Describe              ✗ service not available (fall back to D-Bus)
List machines                    List                  ✗ service not available (fall back to D-Bus)
```

```text title="Output (illustrative, a systemd v258 host)"
FEATURE                          METHOD                STATUS
Describe the service manager     Describe              ✓ supported
List units over Varlink (v258+)  List                  ✓ supported
Change unit properties (v260+)   SetProperties         ✗ method missing on this systemd
Resolve host names               ResolveHostname       ✓ supported
Browse DNS-SD services           BrowseServices        ✓ supported
Dump DNS configuration           DumpDNSConfiguration  ✗ method missing on this systemd
Describe the host                Describe              ✓ supported
List machines                    List                  ✓ supported
```

## The code

```go title="examples/feature-detect/main.go" linenums="1" hl_lines="68 76 41"
--8<-- "examples/feature-detect/main.go"
```

## How it works

- **Two kinds of "no".** A missing socket means the whole service is
  unavailable (too old, or not running); `Supports` returning `false` means the
  service runs but predates the method.
- **Any client works.** Every generated `Client` has `Supports` and `Close`, so
  a two-method interface plus a small generic `wrap` lets one table probe many
  packages.
- **Imports `latest`** so it can name every method; `Supports` asks the live
  service, so the answer reflects the host, not the bindings.
- `Supports` costs one round trip; check once at startup.

## See also

- [Versioning & feature detection](../guides/versioning.md)
- [Errors: falling back](../guides/errors.md#falling-back-when-a-service-is-missing)
