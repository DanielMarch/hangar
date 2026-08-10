// audit.go is the shared app.security_log writer every Phase 15 handler
// that needs to audit a call (support/search's "writes every query to
// app.security_log" chief among them, but also the public mumble-auth
// route's existing pattern in v1/public_mumble_auth.go) can call instead
// of constructing gen.RecordSecurityLogEntryParams by hand.
package middleware

import (
	"context"
	"encoding/json"
	"net/netip"

	"github.com/google/uuid"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// Audit writes one app.security_log row. userID may be uuid.Nil for an
// unauthenticated caller (encodes as a NULL user_id — the row still exists
// so the attempt is not lost). detail is marshaled to JSON; a marshal
// failure degrades to an empty object rather than dropping the audit
// entry. A write failure is logged by the caller's own error handling if
// it chooses to — Audit itself never blocks the response on a
// best-effort security-log write, matching public_mumble_auth.go's
// existing precedent.
func Audit(ctx context.Context, s *store.Store, userID uuid.UUID, action string, target *string, sourceIP string, detail map[string]any) error {
	raw, err := json.Marshal(detail)
	if err != nil {
		raw = []byte(`{}`)
	}
	var uid uuid.NullUUID
	if userID != uuid.Nil {
		uid = uuid.NullUUID{UUID: userID, Valid: true}
	}
	var ip *netip.Addr
	if addr, err := netip.ParseAddr(sourceIP); err == nil {
		ip = &addr
	}
	return s.RecordSecurityLogEntry(ctx, gen.RecordSecurityLogEntryParams{
		UserID:    uid,
		Action:    action,
		Target:    target,
		IpAddress: ip,
		Detail:    raw,
	})
}
