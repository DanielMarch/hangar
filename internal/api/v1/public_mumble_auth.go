package v1

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/hangar-project/hangar/internal/provisioning"
	"github.com/hangar-project/hangar/internal/provisioning/drivers/mumble"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// public_mumble_auth.go is POST /api/v1/public/mumble/auth's entire logic
// (Phase 15 wires the Huma handler on top — the same "documented
// placeholder seam" convention as admin_provisioning.go). This route is
// deliberately the ONLY unauthenticated write route in HANGAR's API
// (01_ARCHITECTURE.md §9.5) — the HMAC signature IS its authentication,
// so verifying it correctly and auditing every call (accepted or
// rejected) matters more here than on any authenticated route, where a
// caller's session/token already establishes identity before this logic
// ever runs.

// MumbleAuthRequest is the signed request body's shape — the HTTP-side
// counterpart for authenticator deployments that call out to HANGAR
// (e.g. the out-of-process Ice bridge, Phase 13's own scope explicitly
// excludes building that bridge, but this is the contract it targets).
type MumbleAuthRequest struct {
	CertificateHash string `json:"certificate_hash"`
}

// MumbleAuthResult is what the future Huma handler translates into an
// HTTP response.
type MumbleAuthResult struct {
	Allowed bool
	UserID  int32
	Name    string
}

// VerifyMumbleAuthSignature checks an HMAC-SHA256-over-the-raw-body
// signature (hex-encoded) against secret, in constant time. A caller that
// gets this wrong (bad signature, wrong secret, tampered body) must be
// treated identically regardless of WHICH check failed — this function
// collapses all of them to a single bool specifically so the caller
// cannot leak which one it was.
func VerifyMumbleAuthSignature(secret string, rawBody []byte, providedSignatureHex string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(rawBody)
	expected := mac.Sum(nil)

	provided, err := hex.DecodeString(providedSignatureHex)
	if err != nil {
		return false
	}
	return hmac.Equal(expected, provided)
}

// mumbleAuthLimiter is a small in-process, IP-keyed fixed-window rate
// limiter guarding this endpoint specifically — defense in depth ahead of
// whatever infrastructure-level rate limiting Phase 15's real HTTP layer
// adds (reverse proxy, generic Huma middleware). Package-level and
// process-wide deliberately: the set of real callers (a Murmur server or
// its Ice bridge companion) is small and fixed, unlike a per-user
// endpoint where per-replica accounting would undercount a distributed
// attacker — here it doesn't need to be Postgres-backed the way Discord's
// installation-wide Cloudflare budget did, because this endpoint's
// abuse surface is "one misbehaving or compromised caller IP", not "one
// externally-enforced installation-wide budget shared across replicas."
var mumbleAuthLimiter = newFixedWindowLimiter(20, time.Minute)

type fixedWindowLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	counts map[string]*windowCount
}

type windowCount struct {
	windowStart time.Time
	count       int
}

func newFixedWindowLimiter(max int, window time.Duration) *fixedWindowLimiter {
	return &fixedWindowLimiter{max: max, window: window, counts: make(map[string]*windowCount)}
}

// Allow reports whether key (the source IP) is still within budget,
// consuming one unit if so.
func (l *fixedWindowLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	wc, ok := l.counts[key]
	if !ok || now.Sub(wc.windowStart) >= l.window {
		wc = &windowCount{windowStart: now, count: 0}
		l.counts[key] = wc
	}
	if wc.count >= l.max {
		return false
	}
	wc.count++
	return true
}

// ErrMumbleAuthRateLimited is returned when sourceIP has exceeded the
// endpoint's own rate limit.
var ErrMumbleAuthRateLimited = fmt.Errorf("v1: mumble auth: rate limited")

// ErrMumbleAuthBadSignature is returned when the HMAC signature doesn't
// verify — treated as a security event (audited), never a plain 400.
var ErrMumbleAuthBadSignature = fmt.Errorf("v1: mumble auth: signature verification failed")

// HandleMumbleAuth is POST /api/v1/public/mumble/auth's logic: rate-limit
// by source IP, verify the HMAC signature, resolve the decision via
// DecideMumbleAuth, and audit every call (accepted or rejected) via
// app.security_log — "must be rate-limited and audited" applies to a
// rejected call at least as much as an accepted one, since a rejected
// call is exactly the signal an attack in progress looks like.
func HandleMumbleAuth(ctx context.Context, s *store.Store, secret string, platformID uuid.UUID, rawBody []byte, signatureHex, sourceIP string) (MumbleAuthResult, error) {
	if !mumbleAuthLimiter.Allow(sourceIP) {
		auditMumbleAuth(ctx, s, sourceIP, "rate_limited", nil)
		return MumbleAuthResult{}, ErrMumbleAuthRateLimited
	}

	if !VerifyMumbleAuthSignature(secret, rawBody, signatureHex) {
		auditMumbleAuth(ctx, s, sourceIP, "bad_signature", nil)
		return MumbleAuthResult{}, ErrMumbleAuthBadSignature
	}

	var req MumbleAuthRequest
	if err := json.Unmarshal(rawBody, &req); err != nil {
		auditMumbleAuth(ctx, s, sourceIP, "malformed_body", nil)
		return MumbleAuthResult{}, fmt.Errorf("v1: mumble auth: decoding body: %w", err)
	}

	decision, err := DecideMumbleAuth(ctx, s, platformID, req.CertificateHash)
	if err != nil {
		auditMumbleAuth(ctx, s, sourceIP, "error", &req.CertificateHash)
		return MumbleAuthResult{}, fmt.Errorf("v1: mumble auth: deciding: %w", err)
	}

	outcome := "denied"
	if decision.Allow {
		outcome = "allowed"
	}
	auditMumbleAuth(ctx, s, sourceIP, outcome, &req.CertificateHash)

	return MumbleAuthResult{Allowed: decision.Allow, UserID: decision.UserID, Name: decision.Name}, nil
}

func auditMumbleAuth(ctx context.Context, s *store.Store, sourceIP, outcome string, certificateHash *string) {
	detail, _ := json.Marshal(map[string]any{"outcome": outcome})
	var target *string
	if certificateHash != nil {
		masked := maskCertificateHash(*certificateHash)
		target = &masked
	}
	var ip *netip.Addr
	if addr, err := netip.ParseAddr(sourceIP); err == nil {
		ip = &addr
	}
	// Best-effort: a security_log write failure must never block the
	// auth decision itself from being returned to the caller (Murmur is
	// waiting on this call to let a real user connect) — logged, not
	// propagated.
	_ = s.RecordSecurityLogEntry(ctx, gen.RecordSecurityLogEntryParams{
		UserID: uuid.NullUUID{}, Action: "mumble.auth." + outcome, Target: target, IpAddress: ip, Detail: detail,
	})
}

// maskCertificateHash keeps only a short, non-reversible-enough prefix in
// the audit trail — the full hash is a persistent client identifier,
// logging it in full on every attempt (including failed ones from
// unrelated callers probing the endpoint) is more retention than the
// audit trail needs.
func maskCertificateHash(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12] + "…"
}

// mumbleDecider adapts DecideMumbleAuth to mumble.Decider for
// authenticator.go's gRPC path — the SAME decision logic
// POST /api/v1/public/mumble/auth uses, so the two authenticator
// deployment modes (direct gRPC vs the HTTP/Ice-bridge counterpart) can
// never disagree about who's allowed to connect.
type mumbleDecider struct {
	Store      *store.Store
	PlatformID uuid.UUID
}

func (d mumbleDecider) Decide(ctx context.Context, certificateHash string) (mumble.Decision, error) {
	return DecideMumbleAuth(ctx, d.Store, d.PlatformID, certificateHash)
}

// NewMumbleDecider returns a mumble.Decider backed by DecideMumbleAuth —
// what cmd/hangar wires into mumble.Authenticator.
func NewMumbleDecider(s *store.Store, platformID uuid.UUID) mumble.Decider {
	return mumbleDecider{Store: s, PlatformID: platformID}
}

// DecideMumbleAuth resolves certificateHash to an allow/deny decision:
// allowed only if it's linked (app.provisioning_state.remote_identity) to
// a HANGAR user, and that user is not currently Strict-Mode-denied
// (internal/provisioning.CheckStrictMode — "any alt" invalid token denies
// the whole user, including here). A certificate hash with no link at all
// is denied, not merely unresolved — there's no meaningful "temporary"
// answer for an identity HANGAR has never seen.
func DecideMumbleAuth(ctx context.Context, s *store.Store, platformID uuid.UUID, certificateHash string) (mumble.Decision, error) {
	link, err := s.GetProvisioningStateByRemoteIdentity(ctx, platformID, &certificateHash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return mumble.Decision{Allow: false}, nil // unlinked certificate — a normal deny, not a system error
		}
		return mumble.Decision{}, fmt.Errorf("v1: mumble auth: looking up certificate hash: %w", err)
	}

	denied, err := provisioning.CheckStrictMode(ctx, s, link.UserID)
	if err != nil {
		return mumble.Decision{}, fmt.Errorf("v1: mumble auth: checking strict mode for user %s: %w", link.UserID, err)
	}
	if denied {
		return mumble.Decision{Allow: false}, nil
	}

	user, err := s.GetUser(ctx, link.UserID)
	if err != nil {
		return mumble.Decision{}, fmt.Errorf("v1: mumble auth: resolving user %s: %w", link.UserID, err)
	}
	return mumble.Decision{Allow: true, Name: user.DisplayName}, nil
}
