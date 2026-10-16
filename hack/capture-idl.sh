#!/usr/bin/env bash
# Capture Varlink IDL snapshots from a running systemd.
#
# Asks every Varlink socket systemd serves for its interfaces and writes
# each description to idl/v<N>/<interface>.varlink, where N is the running
# systemd's major version. Run it on a host, VM or container booted with
# the systemd release you want to snapshot (CI does this per release):
#
#   sudo hack/capture-idl.sh            # writes idl/v$(systemctl --version)
#   sudo hack/capture-idl.sh out/dir    # writes somewhere else
#
# Sockets that are not running are skipped; start the services first (or
# trigger their socket units) to capture their interfaces.
set -euo pipefail

version=$(systemctl --version | awk 'NR==1 {print $2}')
out=${1:-"$(dirname "$0")/../idl/v${version}"}
mkdir -p "$out"

sockets=()
for dir in /run/systemd /run/systemd/userdb /run/systemd/report /run/systemd/machine \
           /run/systemd/netif /run/systemd/resolve /run/systemd/journal /run/systemd/oom /run/udev; do
	[[ -d $dir ]] || continue
	for s in "$dir"/io.systemd.*; do
		[[ -S $s ]] && sockets+=("$s")
	done
done

declare -A seen
for sock in "${sockets[@]}"; do
	ifaces=$(timeout 10 varlinkctl list-interfaces "$sock" 2>/dev/null) || {
		echo "skip $sock (not answering)" >&2
		continue
	}
	for iface in $ifaces; do
		[[ $iface == io.systemd* ]] || continue
		[[ -n ${seen[$iface]:-} ]] && continue
		seen[$iface]=1
		SYSTEMD_COLORS=0 timeout 10 varlinkctl introspect "$sock" "$iface" >"$out/$iface.varlink"
		echo "$iface <- $sock"
	done
done
echo "captured ${#seen[@]} interfaces from systemd v${version} into $out"
