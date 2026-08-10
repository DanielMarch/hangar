package rbac

import (
	"context"

	"github.com/google/uuid"
	"github.com/hangar-project/hangar/internal/store"
)

// PermissionsChangedHook, when set, is invoked by RefreshUser at the end
// of every recomputation — i.e. after every grant.go mutation, inside the
// SAME transaction that mutation and its materialize.go refresh already
// run in (`s` is the Store bound to that in-flight transaction).
//
// This is Phase 11's seam into RBAC without internal/rbac importing
// internal/provisioning (docs/03_IMPLEMENTATION_ROADMAP.md Phase 11: "a
// cleaner seam... avoids a new Phase 11 dependency inside a Phase 10
// package"): internal/provisioning sets this variable in its own init(),
// so the dependency runs one way only (provisioning -> rbac), and
// internal/rbac compiles and tests with zero knowledge that Phase 11
// exists. The default is a no-op, so every Phase 10 test — none of which
// import internal/provisioning — is unaffected.
//
// An RBAC change (direct role assignment/revocation, squad_role add/
// remove, squad membership add/remove, role deletion) is the "RBAC role
// change" trigger from 01_ARCHITECTURE.md §9.2's revocation-event list.
// Note it is deliberately over-inclusive: it fires on every recomputation,
// not just ones that reduce anything, because whether a change is
// entitlement-reducing can only be answered by actually recomputing
// entitlements — that recomputation is the hook implementation's job, not
// this trigger point's.
var PermissionsChangedHook func(ctx context.Context, s *store.Store, userID uuid.UUID) error
