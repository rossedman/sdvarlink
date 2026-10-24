// Package integration runs sdvarlink against real systemd. Run it on a host
// or VM with systemd v257 or newer (CI boots one per supported release):
//
//	go test -tags integration ./integration/
//
// Tests skip when the socket they need does not exist. Only read-only or
// idempotent methods are called; tests that mutate the host are gated
// behind SDVARLINK_MUTATING=1 and meant for disposable VMs.
//
// This file has no build tag so the package builds, and plain go test ./...
// reports it as having no tests, without -tags integration.
package integration
