package provisioning

import (
	"context"
	"sync"
)

// InMemoryDriver is the test-only Driver implementation Phase 11's own
// tests use in place of a real Discord/TeamSpeak/Mumble client (Phase
// 12/13) — being a _test.go file, it never ships in a production binary,
// while still being reachable from `package provisioning_test` integration
// tests in this directory (both are compiled into the same test binary).
//
// Held is keyed by remoteIdentity, each value the current set of groupRefs
// that identity holds according to this fake — so a test can assert
// exactly what a Grant/Revoke sequence left behind, the same way a real
// driver's remote state would look if queried back.
type InMemoryDriver struct {
	mu    sync.Mutex
	Held  map[string]map[string]bool
	Down  bool // when true, Grant/Revoke both fail — simulates the "platform is down" edge case
	Calls []DriverCall
}

// DriverCall records one Grant or Revoke invocation, in order, so a test
// can assert not just the end state but the exact sequence (e.g.
// TestUrgentQueueNotStarvedByBulk timing assertions read Calls to find
// when the urgent call actually landed relative to a slow bulk one).
type DriverCall struct {
	Action         string // "grant" | "revoke"
	RemoteIdentity string
	GroupRef       string
}

// NewInMemoryDriver returns an empty, up fake.
func NewInMemoryDriver() *InMemoryDriver {
	return &InMemoryDriver{Held: make(map[string]map[string]bool)}
}

func (d *InMemoryDriver) Grant(_ context.Context, remoteIdentity, groupRef string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Calls = append(d.Calls, DriverCall{Action: "grant", RemoteIdentity: remoteIdentity, GroupRef: groupRef})
	if d.Down {
		return ErrNoDriver
	}
	if d.Held[remoteIdentity] == nil {
		d.Held[remoteIdentity] = make(map[string]bool)
	}
	d.Held[remoteIdentity][groupRef] = true
	return nil
}

func (d *InMemoryDriver) Revoke(_ context.Context, remoteIdentity, groupRef string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Calls = append(d.Calls, DriverCall{Action: "revoke", RemoteIdentity: remoteIdentity, GroupRef: groupRef})
	if d.Down {
		return ErrNoDriver
	}
	delete(d.Held[remoteIdentity], groupRef)
	return nil
}

// HeldGroups is a small assertion helper: the sorted-by-insertion set of
// groups remoteIdentity currently holds according to this fake.
func (d *InMemoryDriver) HeldGroups(remoteIdentity string) map[string]bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make(map[string]bool, len(d.Held[remoteIdentity]))
	for g := range d.Held[remoteIdentity] {
		out[g] = true
	}
	return out
}
