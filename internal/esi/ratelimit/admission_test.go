package ratelimit

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ── PHASE 20.3: THE STORED CEILING IS THE REAL ONE ──────────────────────
//
// These cover the defect that carried out of 20.2 as Gate 1.3's only
// remaining blocker. §4.4's five-token char-notification reserve was
// implemented by passing the REDUCED ceiling as AcquireRequest.MaxTokens,
// and both ledgers admit by comparing consumption against the ceiling they
// have STORED — so the reduction was persisted into state every caller
// shares. On a live installation that read as a permanent
// esi_ledger_divergence of exactly 5 on char-notification (stored 10,
// server reporting against 15) against a Gate 1.3 tolerance of 1, with
// nothing wrong with the installation.
//
// The property under test is a conjunction, and both halves matter: the
// stored ceiling must be the real one, AND the reserve must still hold. A
// fix that satisfied only the first would close the divergence by deleting
// the reserve.

func soloBucketMaxTokens(t *testing.T, l *LedgerSolo, group, userKey string) (int, bool) {
	t.Helper()
	key := bucketKey(group, userKey)
	sh := shardFor(l.shards, key)
	sh.mu.Lock()
	defer sh.mu.Unlock()
	b, ok := sh.buckets[key]
	if !ok {
		return 0, false
	}
	return b.maxTokens, true
}

// backgroundReq is a char-notification-shaped background call: real ceiling
// 15, admitted against 10.
func backgroundReq(userKey string) AcquireRequest {
	return AcquireRequest{
		Group: "char-notification", UserKey: userKey,
		MaxTokens: 15, AdmissionMaxTokens: 10,
		Window: 15 * time.Minute, RequestTimeout: 30 * time.Second,
	}
}

// interactiveReq is the same bucket with no call-site reduction.
func interactiveReq(userKey string) AcquireRequest {
	return AcquireRequest{
		Group: "char-notification", UserKey: userKey,
		MaxTokens: 15,
		Window:    15 * time.Minute, RequestTimeout: 30 * time.Second,
	}
}

// TestSoloAdmissionCeilingIsNeverStored is the solo half of the fix: a
// per-caller reduction must not reach the bucket, because settledAvailable()
// — Reconcile's view, and the in-memory analogue of the bucket row
// ListLedgerDivergence reads — is measured against it.
func TestSoloAdmissionCeilingIsNeverStored(t *testing.T) {
	ctx := context.Background()
	l := NewLedgerSolo(nil)

	res, err := l.Acquire(ctx, backgroundReq("char:1"))
	require.NoError(t, err)
	require.NoError(t, l.Settle(ctx, res, Cost2XX, time.Now()))

	stored, ok := soloBucketMaxTokens(t, l, "char-notification", "char:1")
	require.True(t, ok, "the acquire must have created a bucket")
	require.Equal(t, 15, stored,
		"the bucket must store the route's REAL ceiling; storing the caller's reduced 10 is what "+
			"made esi_ledger_divergence read a structural 5 on a healthy installation")
}

// TestSoloAdmissionCeilingStillReservesHeadroom is the other half: the
// reserve must survive being moved off the stored ceiling. The background
// caller runs itself out at its own 10; the interactive caller, at that
// exact instant, still gets in against the real 15.
func TestSoloAdmissionCeilingStillReservesHeadroom(t *testing.T) {
	ctx := context.Background()
	l := NewLedgerSolo(nil)
	bg := backgroundReq("char:2")

	admitted := 0
	for i := 0; i < 50; i++ {
		res, err := l.Acquire(ctx, bg)
		if err != nil {
			require.ErrorIs(t, err, ErrRateLimited)
			break
		}
		require.NoError(t, l.Settle(ctx, res, Cost2XX, time.Now()))
		admitted++
	}
	require.Greater(t, admitted, 0, "the background caller must make progress")
	require.Less(t, admitted, 50, "the background caller must eventually be refused by its own ceiling")

	// The reserve, measured at the moment it matters.
	_, err := l.Acquire(ctx, bg)
	require.ErrorIs(t, err, ErrRateLimited, "the background caller is out of its own budget")

	res, err := l.Acquire(ctx, interactiveReq("char:2"))
	require.NoError(t, err, "the interactive caller must find the five reserved tokens")
	require.NotNil(t, res)

	// And the stored ceiling is still the real one after all of that —
	// including after an interactive caller has acquired against it, which
	// is where the pre-20.3 shape produced a max_tokens flip per call.
	stored, ok := soloBucketMaxTokens(t, l, "char-notification", "char:2")
	require.True(t, ok)
	require.Equal(t, 15, stored)
}

// TestAdmissionCeilingNeverExceedsTheStoredCeiling pins the clamp. A caller
// asking to be admitted against MORE than the bucket holds must not get it:
// the stored ceiling is the truth, and AdmissionMaxTokens can only ever
// hold tokens back.
func TestAdmissionCeilingNeverExceedsTheStoredCeiling(t *testing.T) {
	ctx := context.Background()
	l := NewLedgerSolo(nil)

	greedy := AcquireRequest{
		Group: "char-notification", UserKey: "char:3",
		MaxTokens: 15, AdmissionMaxTokens: 1000,
		Window: 15 * time.Minute, RequestTimeout: 30 * time.Second,
	}

	admitted := 0
	for i := 0; i < 100; i++ {
		res, err := l.Acquire(ctx, greedy)
		if err != nil {
			require.ErrorIs(t, err, ErrRateLimited)
			break
		}
		require.NoError(t, l.Settle(ctx, res, Cost2XX, time.Now()))
		admitted++
	}
	require.Less(t, admitted, 100,
		"an AdmissionMaxTokens above the stored ceiling must be clamped to it, not honoured")

	stored, ok := soloBucketMaxTokens(t, l, "char-notification", "char:3")
	require.True(t, ok)
	require.Equal(t, 15, stored, "a greedy admission ceiling must not raise the stored one either")
}

// TestZeroAdmissionMaxTokensMeansNoReduction is the default every caller
// but the char-notification poller relies on: an unset field must behave
// exactly as it did before the field existed.
func TestZeroAdmissionMaxTokensMeansNoReduction(t *testing.T) {
	req := AcquireRequest{MaxTokens: 600}
	require.Equal(t, 600, req.admissionCeiling())

	req.AdmissionMaxTokens = 595
	require.Equal(t, 595, req.admissionCeiling())

	// Negative is treated as unset rather than as a ceiling of zero: a
	// ceiling of zero would refuse every call, and a caller computing a
	// reduction has no legitimate way to mean that.
	req.AdmissionMaxTokens = -1
	require.Equal(t, 600, req.admissionCeiling())
}
