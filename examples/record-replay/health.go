// Package health checks that systemd-journald answers over Varlink. Its
// test replays a golden file recorded against a real journald, so it runs
// anywhere; pass -update on a systemd host to re-record:
//
//	go test ./examples/record-replay/                 # replay
//	sudo go test ./examples/record-replay/ -update    # record against journald
package health

import (
	"context"
	"fmt"
	"slices"

	"github.com/rossedman/sdvarlink/systemd/v257/journal"
	"github.com/rossedman/sdvarlink/varlink"
)

// Report describes the journald instance that answered.
type Report struct {
	Product string
	Version string
}

// CheckJournal verifies journald implements io.systemd.Journal and that a
// Synchronize round trip succeeds.
func CheckJournal(ctx context.Context, c *journal.Client) (*Report, error) {
	info, err := varlink.GetInfo(ctx, c.Caller())
	if err != nil {
		return nil, err
	}
	if !slices.Contains(info.Interfaces, journal.InterfaceName) {
		return nil, fmt.Errorf("health: %s does not implement %s", info.Product, journal.InterfaceName)
	}
	if err := c.Synchronize(ctx); err != nil {
		return nil, fmt.Errorf("health: synchronize: %w", err)
	}
	return &Report{Product: info.Product, Version: info.Version}, nil
}
