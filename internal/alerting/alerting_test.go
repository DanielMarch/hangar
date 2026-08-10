package alerting_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/hangar-project/hangar/internal/alerting"
	"github.com/hangar-project/hangar/internal/alerting/channels"
	"github.com/hangar-project/hangar/internal/alerting/render"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/stretchr/testify/require"
)

func mustUUID(s string) uuid.UUID { return uuid.MustParse(s) }

// TestDedupeHashStableAcrossRestart is §4.4's "hash-based deduplication
// stable across process restarts", tested for the two ways it actually
// breaks in Go.
//
// A whole-payload serialisation would pass a naive test and fail in
// production, because map iteration order is randomised PER PROCESS: the
// hash would be stable within one run and different after a restart, which
// re-delivers every alert the operator has already read. The loop below
// hashes repeatedly (fresh maps each time, in different insertion orders)
// so an unsorted iteration shows up as a mismatch rather than as luck.
func TestDedupeHashStableAcrossRestart(t *testing.T) {
	target := alerting.Target{Kind: "squad", Ref: "42"}

	want := alerting.NotificationFingerprint("StructureUnderAttack", 1234567890, target).Hash()
	require.Len(t, want, 64, "a hex SHA-256 digest")

	for i := 0; i < 200; i++ {
		got := alerting.NotificationFingerprint("StructureUnderAttack", 1234567890, target).Hash()
		require.Equal(t, want, got, "the same notification must hash identically every time")
	}

	// Field insertion order must not matter — this is the direct proxy for
	// "the same fingerprint built in a different process".
	a := alerting.Fingerprint{AlertType: "x", Fields: map[string]string{}}
	for _, k := range []string{"alpha", "beta", "gamma", "delta", "epsilon"} {
		a.Fields[k] = k + "-value"
	}
	b := alerting.Fingerprint{AlertType: "x", Fields: map[string]string{}}
	for _, k := range []string{"epsilon", "delta", "gamma", "beta", "alpha"} {
		b.Fields[k] = k + "-value"
	}
	require.Equal(t, a.Hash(), b.Hash(), "map insertion order must not reach the digest")

	// Distinctness: different notification, target, or alert type must all
	// produce different hashes, or dedupe would swallow real alerts.
	require.NotEqual(t, want, alerting.NotificationFingerprint("StructureUnderAttack", 1234567891, target).Hash())
	require.NotEqual(t, want, alerting.NotificationFingerprint("StructureLostShields", 1234567890, target).Hash())
	require.NotEqual(t, want, alerting.NotificationFingerprint("StructureUnderAttack", 1234567890, alerting.Target{Kind: "squad", Ref: "43"}).Hash())

	// Length-prefixing: two field sets that concatenate to the same bytes
	// must not collide. Without it, {"ab":"c"} and {"a":"bc"} would.
	collideA := alerting.Fingerprint{AlertType: "t", Fields: map[string]string{"ab": "c"}}
	collideB := alerting.Fingerprint{AlertType: "t", Fields: map[string]string{"a": "bc"}}
	require.NotEqual(t, collideA.Hash(), collideB.Hash(), "component boundaries must be unambiguous")

	// No timestamp may be involved: hashing now and hashing later must
	// agree, which is the whole point of excluding occurred_at.
	first := alerting.ThresholdFingerprint("corporation.starbase.fuel_low", "starbase", 99, "2026-08-12T00:00:00Z", target).Hash()
	time.Sleep(2 * time.Millisecond)
	second := alerting.ThresholdFingerprint("corporation.starbase.fuel_low", "starbase", 99, "2026-08-12T00:00:00Z", target).Hash()
	require.Equal(t, first, second)
}

// TestSemanticFieldsIgnoresPayloadOrdering proves the payload-derived
// fingerprint path is order-independent too: the same JSON object with its
// keys written in a different order yields the same fields.
func TestSemanticFieldsIgnoresPayloadOrdering(t *testing.T) {
	one := json.RawMessage(`{"structureID":1021975179626,"solarsystemID":30000142,"nested":{"b":2,"a":1}}`)
	two := json.RawMessage(`{"nested":{"a":1,"b":2},"solarsystemID":30000142,"structureID":1021975179626}`)

	fa := alerting.SemanticFields(one, "structureID", "solarsystemID", "nested")
	fb := alerting.SemanticFields(two, "structureID", "solarsystemID", "nested")
	require.Equal(t, fa, fb)

	// A key the payload does not carry is "absent", not an error.
	require.Equal(t, "", alerting.SemanticFields(one, "notThere")["notThere"])
	require.NotPanics(t, func() { alerting.SemanticFields(json.RawMessage(`"not an object"`), "x") })
	require.NotPanics(t, func() { alerting.SemanticFields(nil, "x") })
}

// TestCoalesceKeyRoundTripsHostileRefs pins the escaping. Target refs are
// external ids and alert types are an open vocabulary, so a component
// containing "|" or "\" must round-trip exactly and must not be able to
// forge membership of another group.
//
// Written specifically because Phase 13 found an escape-sequence
// ambiguity in the TeamSpeak driver: the failure mode is that `a\|b` (one
// literal pipe) and `a\\|b` (a backslash then a separator) decode
// identically if unescaping is done with naive replacement instead of a
// single left-to-right pass.
func TestCoalesceKeyRoundTripsHostileRefs(t *testing.T) {
	bucket := time.Date(2026, 8, 10, 12, 5, 0, 0, time.UTC)

	hostile := []alerting.Target{
		{Kind: "squad", Ref: "42"},
		{Kind: "squad", Ref: `a|b`},
		{Kind: "squad", Ref: `a\b`},
		{Kind: "squad", Ref: `a\|b`},
		{Kind: "squad", Ref: `a\\|b`},
		{Kind: "installation", Ref: ""},
		{Kind: "user", Ref: "☃|☃"},
	}

	seen := make(map[string]alerting.Target, len(hostile))
	for _, target := range hostile {
		key := alerting.CoalesceKey{Target: target, AlertType: "Structure|UnderAttack", Bucket: bucket}
		encoded := key.String()

		other, clash := seen[encoded]
		require.False(t, clash, "%#v and %#v must not encode to the same coalescing key %q", target, other, encoded)
		seen[encoded] = target

		parsed, ok := alerting.ParseCoalesceKey(encoded)
		require.True(t, ok, "key %q must parse back", encoded)
		require.Equal(t, target, parsed.Target)
		require.Equal(t, "Structure|UnderAttack", parsed.AlertType)
		require.True(t, parsed.Bucket.Equal(bucket))
		require.True(t, parsed.Coalesces())
	}

	// A zero window disables coalescing: the routing identity survives so
	// mentions still resolve, but the key does not group.
	off := alerting.NewCoalesceKey(alerting.Target{Kind: "squad", Ref: "42"}, "StructureUnderAttack", time.Now(), 0)
	parsed, ok := alerting.ParseCoalesceKey(off.String())
	require.True(t, ok)
	require.False(t, parsed.Coalesces(), "a zero window must not group events")
	require.Equal(t, "squad", parsed.Target.Kind)

	// Unparseable input is not an error the dispatcher should die on.
	_, ok = alerting.ParseCoalesceKey("")
	require.False(t, ok)
	_, ok = alerting.ParseCoalesceKey("only|three|parts")
	require.False(t, ok)
}

// TestCoalesceWindowBucketsAreShared is the property the fixed window
// exists for: every event inside one window gets the same key and
// therefore the same deadline, so a burst arrives at the pump as one
// group.
func TestCoalesceWindowBucketsAreShared(t *testing.T) {
	target := alerting.Target{Kind: "corporation", Ref: "98000001"}
	window := 5 * time.Minute
	base := time.Date(2026, 8, 10, 12, 1, 30, 0, time.UTC)

	first := alerting.NewCoalesceKey(target, "StructureUnderAttack", base, window)
	for i := 0; i < 40; i++ {
		// Events spread across the remainder of the same window.
		at := base.Add(time.Duration(i) * 4 * time.Second)
		require.Equal(t, first.String(), alerting.NewCoalesceKey(target, "StructureUnderAttack", at, window).String(),
			"every event inside one window shares one key")
	}
	require.Equal(t, base.Truncate(window).Add(window), first.Due(window))

	// A different target, or a different alert type, is a different group.
	require.NotEqual(t, first.String(),
		alerting.NewCoalesceKey(alerting.Target{Kind: "corporation", Ref: "98000002"}, "StructureUnderAttack", base, window).String())
	require.NotEqual(t, first.String(),
		alerting.NewCoalesceKey(target, "StructureLostShields", base, window).String())

	// The documented cost of a fixed window: a burst straddling the edge
	// makes two groups. Asserted so the behaviour is a decision on record,
	// not a surprise.
	next := alerting.NewCoalesceKey(target, "StructureUnderAttack", base.Truncate(window).Add(window), window)
	require.NotEqual(t, first.String(), next.String())
}

// TestRollupTruncatesWithRemainderCount covers §4.4's "truncated with an
// explicit remainder count, never dropped", at both channels' real limits.
func TestRollupTruncatesWithRemainderCount(t *testing.T) {
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = fmt.Sprintf("structure Nakugard XI - Moon 3 - Astrahus #%02d — system 30002053 — attacker Test Corp", i)
	}
	header := render.Header("Structure under attack", len(lines))
	require.Contains(t, header, "40 events")

	for _, tc := range []struct {
		name  string
		limit int
	}{
		{"discord", channels.DiscordContentLimit},
		{"slack", channels.SlackSectionTextLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := render.Rollup(header, lines, tc.limit)
			require.LessOrEqual(t, utf8.RuneCountInString(body), tc.limit,
				"the rendered roll-up must fit the channel's limit")
			require.Contains(t, body, "… and ", "truncation must be explicit")
			require.Contains(t, body, "more")
			require.Contains(t, body, lines[0], "the OLDEST event must survive truncation")

			// The remainder count must be the true number omitted.
			shown := 0
			for _, line := range lines {
				if strings.Contains(body, line) {
					shown++
				}
			}
			require.Contains(t, body, fmt.Sprintf("… and %d more", len(lines)-shown),
				"the remainder count must equal the number of events actually omitted")
		})
	}

	// Under the limit, nothing is truncated and no remainder appears.
	short := render.Rollup(header, lines[:3], channels.SlackSectionTextLimit)
	require.NotContains(t, short, "… and")
	for _, line := range lines[:3] {
		require.Contains(t, short, line)
	}

	// Multi-byte content is counted in runes, not bytes: a body of legal
	// length must not be truncated just because it is not ASCII.
	wide := make([]string, 5)
	for i := range wide {
		wide[i] = strings.Repeat("☃", 100)
	}
	body := render.Rollup("Header", wide, 600)
	require.NotContains(t, body, "… and", "600 runes of content must fit a 600-rune limit")
}

// TestGenericRendererIsTheLastResort proves the template chain falls
// through to Phase 9's generic key/value renderer for a type no template
// knows — the render half of §4.4's generic-fallback rule. (The full
// end-to-end criterion, including the unknown-types board and a real
// delivery, is TestUnrecognisedTypeUsesGenericRenderer in the integration
// suite.)
func TestGenericRendererIsTheLastResort(t *testing.T) {
	const unknownType = "SomeTypeCCPInventedLastTuesday"
	require.False(t, render.HasTemplate(unknownType), "the premise: no template exists for this type")

	payload := json.RawMessage(`{"whoKnows":"a new field","count":3,"nested":{"a":1}}`)

	full := render.Render(unknownType, payload)
	require.Equal(t, render.Generic(payload), full,
		"an unknown type must render through generic.go verbatim, not through a rewritten copy of it")
	require.Contains(t, full, "whoKnows: a new field")
	require.Contains(t, full, "count: 3")

	// The one-line form used inside a roll-up carries the same content.
	line := render.Line(unknownType, payload)
	require.NotContains(t, line, "\n", "a roll-up line must be a single line")
	require.Contains(t, line, "whoKnows: a new field")

	// The parse-failure shape (migration 00035's {"raw": ...}) renders too
	// — this is the input side of the fallback, already produced by
	// internal/sync/handlers.parseNotificationYAML.
	raw := json.RawMessage(`{"raw":"unquoted: value: with colon breaking parse"}`)
	require.Contains(t, render.Render(unknownType, raw), "unquoted: value: with colon breaking parse")

	// And nothing about an empty or malformed payload panics or errors.
	require.NotPanics(t, func() { render.Render(unknownType, nil) })
	require.NotPanics(t, func() { render.Render(unknownType, json.RawMessage(`[1,2,3]`)) })
	require.Equal(t, "(no payload)", render.Render(unknownType, nil))
}

// TestRetryPolicyBacksOffThenDeadLetters covers the decision half of
// §4.4's "retry with backoff and eventually dead-letter". The database
// half is TestDeadLetterAfterMaxAttempts in the integration suite.
func TestRetryPolicyBacksOffThenDeadLetters(t *testing.T) {
	policy := alerting.RetryPolicy{MaxAttempts: 5, Base: time.Minute, Cap: time.Hour}
	now := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	transient := errors.New("connection refused")

	// Attempts 1-4 retry, with each delay double the last.
	var delays []time.Duration
	for attemptsBefore := 0; attemptsBefore < 4; attemptsBefore++ {
		d := policy.Decide(attemptsBefore, now, transient, now)
		require.False(t, d.DeadLetter, "attempt %d must retry, not dead-letter", attemptsBefore+1)
		require.True(t, d.NextAttemptAt.After(now))
		delays = append(delays, d.NextAttemptAt.Sub(now))
	}
	require.Equal(t, []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute}, delays)

	// The fifth attempt is the last.
	final := policy.Decide(4, now, transient, now)
	require.True(t, final.DeadLetter, "the delivery must dead-letter once the attempt budget is spent")
	require.Contains(t, final.Reason, "exhausted 5 attempts")
	require.Contains(t, final.Reason, "connection refused", "the board must say WHY, not just that it failed")

	// The backoff is capped.
	require.Equal(t, time.Hour, policy.Backoff(50))

	// A permanent failure dead-letters immediately rather than burning the
	// budget re-proving the same 404.
	permanent := &channels.PermanentError{Reason: "discord: webhook returned 404: unknown webhook"}
	immediate := policy.Decide(0, now, permanent, now)
	require.True(t, immediate.DeadLetter)
	require.Contains(t, immediate.Reason, "not retryable")

	// Defaults are usable without configuration.
	var zero alerting.RetryPolicy
	require.False(t, zero.Decide(0, now, transient, now).DeadLetter)
	require.True(t, zero.Decide(alerting.DefaultMaxAttempts-1, now, transient, now).DeadLetter)
}

// TestRoutingGroupsByTargetDeterministically covers the routing half of
// the coalescing key: destinations are grouped per target, duplicates
// collapse, and the ordering is stable so two replicas enqueue the same
// rows in the same order.
func TestRoutingGroupsByTargetDeterministically(t *testing.T) {
	channelA := mustUUID("11111111-1111-4111-8111-111111111111")
	channelB := mustUUID("22222222-2222-4222-8222-222222222222")
	squad, corp := "42", "98000001"
	mention := "<!here>"

	rules := []gen.AppAlertRoutingRule{
		{AlertType: "StructureUnderAttack", TargetKind: "squad", TargetRef: &squad, ChannelID: channelB, Enabled: true},
		{AlertType: "StructureUnderAttack", TargetKind: "corporation", TargetRef: &corp, ChannelID: channelA, Mention: &mention, Enabled: true},
		{AlertType: "StructureUnderAttack", TargetKind: "squad", TargetRef: &squad, ChannelID: channelA, Enabled: true},
		// A duplicate (same target, same channel) must collapse.
		{AlertType: "StructureUnderAttack", TargetKind: "squad", TargetRef: &squad, ChannelID: channelA, Enabled: true},
		// A disabled rule must be ignored entirely.
		{AlertType: "StructureUnderAttack", TargetKind: "alliance", TargetRef: &corp, ChannelID: channelA, Enabled: false},
	}

	routing := alerting.GroupRules(rules)
	require.False(t, routing.IsEmpty())
	require.Equal(t, []alerting.Target{
		{Kind: "corporation", Ref: corp},
		{Kind: "squad", Ref: squad},
	}, routing.Targets, "targets must be ordered deterministically (kind, then ref)")

	require.Len(t, routing.Destinations[alerting.Target{Kind: "squad", Ref: squad}], 2,
		"the duplicate rule must collapse into one destination")
	require.Equal(t, mention, routing.Destinations[alerting.Target{Kind: "corporation", Ref: corp}][0].Mention)

	require.True(t, alerting.GroupRules(nil).IsEmpty())
}
