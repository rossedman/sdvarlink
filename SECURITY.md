# Security policy

## Supported versions

Security fixes go into the latest minor release. Before v1.0, that is the
newest `v0.x` tag.

## Reporting a vulnerability

Please report vulnerabilities privately through
[GitHub security advisories](https://github.com/rossedman/sdvarlink/security/advisories/new),
not in public issues. Include a description, affected versions and, if you
can, a reproducer. You can expect an acknowledgement within a week.

## Verifying releases

The library is installed with `go get`, which checks every module against the
Go checksum database, so most users need nothing more. The `sdvarlink-gen`
archives on each GitHub release come with a `checksums.txt` signed keylessly
with [cosign](https://docs.sigstore.dev/) by the release workflow, and a build
provenance attestation. To verify a downloaded archive:

```sh
cosign verify-blob checksums.txt \
  --bundle checksums.txt.sigstore.json \
  --certificate-identity-regexp '^https://github.com/rossedman/sdvarlink/.github/workflows/release.yml@refs/tags/v' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
sha256sum --ignore-missing -c checksums.txt
gh attestation verify sdvarlink-gen_*_linux_amd64.tar.gz --repo rossedman/sdvarlink
```

## Security model

sdvarlink is a client library. It adds no privileges of its own:

- **Authorization stays with systemd.** Services check the caller's
  credentials (`SO_PEERCRED`) and polkit. sdvarlink never escalates.
- **Resource limits.** Messages over 16 MiB (`varlink.WithMaxFrameSize`) and
  more than 16 descriptors per read (`varlink.WithMaxFiles`) are rejected,
  so a hostile peer cannot exhaust memory or descriptors.
- **Descriptor hygiene.** Received descriptors get `FD_CLOEXEC`. Any
  descriptor no field claims, or that arrives with a failed message, is
  closed.
- **Strict decoding.** Field types are checked; unknown fields are kept in
  `Extra` rather than rejected, so newer systemd releases stay readable.
- **No logging.** sdvarlink never logs call parameters, which can carry
  user records or credentials.

Bugs in these guarantees are security bugs; please report them as above.
