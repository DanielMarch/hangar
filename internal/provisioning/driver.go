package provisioning

import (
	"context"
	"fmt"
	"sync"
)

// Driver is the interface every platform driver implements — Discord
// (Phase 12), TeamSpeak and Mumble (Phase 13). Grant/Revoke operate on one
// remote group at a time, keyed by app.platform_group.remote_ref (a
// Discord role id, TS3 server group id, or Mumble ACL group name) against
// one linked remote identity (app.provisioning_state.remote_identity).
// Computing WHICH groups to add/remove is reconcile.go/urgent.go's job
// (the diff against desired_groups is already what goes into
// app.provisioning_audit's groups_added/groups_removed) — a driver never
// sees the full desired set, only one grant or revoke at a time, so a
// driver implementation never needs its own notion of "current state".
type Driver interface {
	// Grant adds remoteIdentity to groupRef on the remote platform. Must
	// be idempotent — calling it for a group the identity already holds
	// is not an error (a retried revocation-then-regrant, or a bulk
	// reconcile re-running after a partial failure, must not fail here).
	Grant(ctx context.Context, remoteIdentity, groupRef string) error

	// Revoke removes remoteIdentity from groupRef. Also idempotent —
	// removing a group the identity never held is not an error, which is
	// what makes a retried revocation against a platform that came back
	// up safe to simply retry wholesale rather than needing to know which
	// half of a partial failure already landed.
	Revoke(ctx context.Context, remoteIdentity, groupRef string) error
}

// Drivers resolves the Driver for a platform by platform_id. Phase 11 has
// no real drivers (Discord/TeamSpeak/Mumble are Phase 12/13) — this is
// purely the registration point cmd/hangar/work.go wires real drivers into
// once they exist, and what Phase 11's own tests populate with
// InMemoryDriver. A platform with no registered driver is treated as
// "down": Grant/Revoke both return an error, so a revocation against it
// retries and stays visible on the exposure board rather than being
// silently skipped (roadmap edge case: "a revocation against a platform
// that is down must retry and remain visible... never marked complete").
type Drivers struct {
	mu   sync.RWMutex
	byID map[string]Driver
}

// NewDrivers returns an empty registry — callers Register platform_id ->
// Driver pairs as drivers become available (real ones in Phase 12/13, the
// in-memory fake in tests).
func NewDrivers() *Drivers {
	return &Drivers{byID: make(map[string]Driver)}
}

// Register associates a Driver with a platform_id (string form, so this
// package's callers don't need to import uuid just to populate the
// registry from a config/bootstrap path).
func (d *Drivers) Register(platformID string, driver Driver) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.byID[platformID] = driver
}

// ErrNoDriver is returned by Lookup for a platform_id with nothing
// registered — the "platform is down" case from this file's doc comment.
var ErrNoDriver = fmt.Errorf("provisioning: no driver registered for platform")

// Lookup returns the Driver for platformID, or ErrNoDriver.
func (d *Drivers) Lookup(platformID string) (Driver, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	driver, ok := d.byID[platformID]
	if !ok {
		return nil, ErrNoDriver
	}
	return driver, nil
}
