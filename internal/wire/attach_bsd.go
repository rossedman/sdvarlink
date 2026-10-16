//go:build unix && !linux

package wire

// lastStartRule selects Linux's descriptor attribution; see Transport.
const lastStartRule = false
