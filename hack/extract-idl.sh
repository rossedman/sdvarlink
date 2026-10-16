#!/usr/bin/env bash
# Extract Varlink IDL snapshots from a systemd source release, without
# booting it. Used to add a release to idl/ and by the weekly drift job.
#
#   hack/extract-idl.sh 261          # clones systemd v261, writes idl/v261
set -euo pipefail

version=${1:?usage: extract-idl.sh VERSION}
root=$(cd "$(dirname "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

git clone --quiet --depth 1 --branch "v${version}" --filter=blob:none --sparse \
	https://github.com/systemd/systemd "$work/systemd"
git -C "$work/systemd" sparse-checkout set src/shared src/libsystemd/sd-varlink
rm -rf "$root/idl/v${version}"
python3 "$root/hack/idl-from-source/extract.py" --src "$work/systemd" --version "$version" --out "$root/idl/v${version}"
