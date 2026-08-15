package alerting

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/hangar-project/hangar/internal/alerting/catalogue"
	"github.com/hangar-project/hangar/internal/store"
)

// ── PHASE 20.4, DEFECT B25: THE THRESHOLD CATEGORY GETS A PRODUCER ───────
//
// 00_SRS_v3.1.md §4.4 defines three ways an alert comes into being, and
// app.alert_type.category names all three: `esi_notification` (CCP told
// us), `domain_event` (HANGAR observed something about itself), and
// `threshold` (HANGAR watched synced data cross a boundary). The catalogue
// has declared four thresholds since Phase 14, each with the source route
// §4.4 makes mandatory, and until this file nothing evaluated any of them.
//
// That is not a gap of the ordinary "unfinished feature" kind, and §4.4
// says so itself in the sentence that makes the source-route declaration a
// BUILD-TIME error: "a threshold alert with no data source silently
// generates zero alerts, which a drop test cannot detect." The same
// sentence describes an unevaluated threshold exactly. Gate 3's four-hour
// run drops zero alerts if zero alerts exist, and a green Gate 3 over an
// empty pipeline is the most expensive kind of false pass there is.
//
// ── WHY A PERIODIC PASS AND NOT A SYNC-TIME HOOK ─────────────────────────
// The notification producer hangs off the sync handler that writes the row
// (see cmd/hangar/alerting.go's wireAlertGeneration), because a
// notification IS an event: it arrives once, at a knowable instant. A
// threshold is not an event. "Fuel expires in under 48 hours" becomes true
// with NO WRITE AT ALL — the row does not change, the clock moves — so
// there is no sync-time moment to hook. Only a pass that re-asks the
// question can notice, which is why this is a ticker and why its cadence
// is a floor on how late an alert can be, not on how often it fires.
//
// Firing repeatedly is prevented by the re-arm token, not by the cadence:
// every subject's fingerprint includes the field whose change means "this
// is a new occurrence" (a refuel, a logon, a new contract), so a pass that
// finds the same 40 structures still low writes 40 fingerprints that
// already exist and app.alert_event's UNIQUE constraint absorbs them. That
// is deduplication doing its job, and it is counted — Gate 3.1's
// `suppressed_by_dedupe` term is exactly this.

// ThresholdPolicy is the operator-tunable margin for each of §4.4's four
// threshold alerts. Every zero value selects the default beside it; an
// installation that wants none of them switches the alert type off in
// app.alert_type or routes it nowhere, which is the operator-facing
// control, rather than tuning a margin to infinity.
type ThresholdPolicy struct {
	// StructureFuelWithin fires corporation.structure.fuel_low for a
	// structure whose fuel_expires falls inside this window. 48h by
	// default: two days is enough notice to organise a fuel run in most
	// time zones, and short enough that the alert still means something.
	StructureFuelWithin time.Duration

	// StarbaseFuelBelow fires corporation.starbase.fuel_low when the
	// total quantity of fuel blocks in the bay falls to or below this.
	//
	// A quantity rather than a duration because a starbase reports its
	// fuel bay's CONTENTS, not an expiry: burn rate depends on tower size,
	// which lives in the SDE and is not read here. 2000 blocks by default
	// — a large tower burns 40/hour, so that is roughly two days of fuel,
	// deliberately the same order of notice as the structure threshold.
	StarbaseFuelBelow int64

	// StarbaseFuelBand is how coarsely the remaining quantity is bucketed
	// to form the re-arm token, and it exists because the starbase
	// threshold is the one case with no natural one.
	//
	// The other three thresholds re-arm on a value that MOVES WHEN THE
	// SITUATION CHANGES and not otherwise (a refuel pushes fuel_expires
	// out, a logon replaces logoff_date, a new contract has a new expiry).
	// A fuel quantity does neither: it decreases continuously, so using it
	// raw would fire an alert per sync pass all the way down, and using a
	// constant would fire once and then stay silent through a refuel and a
	// second drain.
	//
	// Banding is the honest middle: the alert re-fires each time the tower
	// drops a whole band, and a refuel above the threshold stops it
	// entirely. 500 blocks by default, so a tower at the 2000 default
	// alerts about four times on its way down rather than once or
	// hundreds of times.
	StarbaseFuelBand int64

	// MemberInactiveFor fires corporation.member.inactive for a member
	// whose last logoff is older than this. 90 days by default: long
	// enough that a holiday or a deployment does not put somebody on the
	// list, which is what makes the list worth reading.
	MemberInactiveFor time.Duration

	// ContractExpiringWithin fires corporation.contract.expiring for an
	// outstanding contract expiring inside this window. 72h by default —
	// longer than the fuel margin because extending or fulfilling a
	// contract can need another party's cooperation.
	ContractExpiringWithin time.Duration
}

// Threshold policy defaults. Named constants rather than literals inside
// the accessors so .env.example, the config loader and this package can
// all cite one number.
const (
	DefaultStructureFuelWithin    = 48 * time.Hour
	DefaultStarbaseFuelBelow      = int64(2000)
	DefaultStarbaseFuelBand       = int64(500)
	DefaultMemberInactiveFor      = 90 * 24 * time.Hour
	DefaultContractExpiringWithin = 72 * time.Hour
)

func (p ThresholdPolicy) structureFuelWithin() time.Duration {
	if p.StructureFuelWithin <= 0 {
		return DefaultStructureFuelWithin
	}
	return p.StructureFuelWithin
}

func (p ThresholdPolicy) starbaseFuelBelow() int64 {
	if p.StarbaseFuelBelow <= 0 {
		return DefaultStarbaseFuelBelow
	}
	return p.StarbaseFuelBelow
}

func (p ThresholdPolicy) starbaseFuelBand() int64 {
	if p.StarbaseFuelBand <= 0 {
		return DefaultStarbaseFuelBand
	}
	return p.StarbaseFuelBand
}

func (p ThresholdPolicy) memberInactiveFor() time.Duration {
	if p.MemberInactiveFor <= 0 {
		return DefaultMemberInactiveFor
	}
	return p.MemberInactiveFor
}

func (p ThresholdPolicy) contractExpiringWithin() time.Duration {
	if p.ContractExpiringWithin <= 0 {
		return DefaultContractExpiringWithin
	}
	return p.ContractExpiringWithin
}

// Evaluator runs §4.4's threshold category: one pass re-asks each
// threshold's question of the synced data and emits for every subject on
// the wrong side of the line.
type Evaluator struct {
	Pool    store.Pool
	Emitter *Emitter
	Policy  ThresholdPolicy
	Now     func() time.Time
	Log     *slog.Logger

	preflightOnce sync.Once
}

// reportUnpollableThresholds says, once, which thresholds this
// installation cannot possibly fire.
//
// §4.4 makes "a threshold alert whose source route is not in the sync set"
// a BUILD-TIME error, and catalogue.ValidateThresholds enforces exactly
// that under `make check-alert-sources`. This is the half a build cannot
// check: the route is in the sync set and in the catalogue, and THIS
// INSTALLATION has no enabled subscription to it — because no character
// has granted the scope, or because the pin blocks the route, or because
// the catalogue has not been ingested yet.
//
// The consequence is the one §4.4 spends a whole sentence on: the alert
// "silently generates zero alerts", which looks exactly like an
// installation where nothing is wrong. Saying so once at startup is the
// difference between an operator knowing their fuel alerts cannot fire and
// an operator believing their structures are fine.
//
// It never fails a pass. A threshold whose data is missing today may have
// it tomorrow — a character authorises, the catalogue ingests — and
// refusing to evaluate the other three because one is unpollable would be
// a worse failure than the one being reported.
func (e *Evaluator) reportUnpollableThresholds(ctx context.Context) {
	if e.Log == nil {
		return
	}
	paths := catalogue.ThresholdSourceRoutes()
	if len(paths) == 0 {
		return
	}
	rows, err := store.New(e.Pool).CountEnabledSubscriptionsForRoutePaths(ctx, paths)
	if err != nil {
		e.Log.WarnContext(ctx, "alerting: could not check whether the threshold source routes are being polled", "error", err)
		return
	}
	// Which alert types depend on each path, so the log line names the
	// ALERT an operator will not receive rather than only the route they
	// have never heard of.
	dependants := map[string][]string{}
	for _, t := range catalogue.Thresholds() {
		dependants[t.SourceRoute] = append(dependants[t.SourceRoute], t.Name)
	}
	for _, row := range rows {
		if row.EnabledSubscriptions > 0 {
			continue
		}
		reason := "no enabled sync subscription — no character has granted the scope this route needs"
		if !row.RouteCatalogued {
			reason = "the route is not in the catalogue, or is blocked by the compatibility pin"
		}
		e.Log.WarnContext(ctx,
			"alerting: a threshold alert's source route is not being polled on this installation — "+
				"it will generate zero alerts, which is indistinguishable from nothing being wrong",
			"upstream_path", row.UpstreamPath, "alert_types", dependants[row.UpstreamPath], "reason", reason)
	}
}

// EvaluationResult summarises one pass, per alert type and in total. Like
// EmitResult, none of its fields is an error: a pass that finds nothing
// over the line is the healthy, common outcome.
type EvaluationResult struct {
	// Subjects counts rows that crossed a threshold — the number of
	// questions answered "yes", before routing or deduplication.
	Subjects int
	// Emitted counts alert events actually written.
	Emitted int
	// Deduplicated counts subjects whose event already existed: the
	// threshold is still crossed and was already reported. On a steady
	// installation this is the largest number here, by design.
	Deduplicated int
	// Unrouted counts alert types nobody has subscribed to, which were
	// therefore not evaluated at all.
	Unrouted int
	// ByType breaks Subjects down for logging.
	ByType map[string]int
}

// Evaluate runs one pass over all four thresholds.
//
// A failure evaluating ONE threshold does not abandon the others: they are
// independent questions over independent tables, and a broken corporation
// contract sync must not silence the fuel alerts. The first error is
// returned once every threshold has had its turn, so a caller still learns
// something went wrong.
func (e *Evaluator) Evaluate(ctx context.Context) (EvaluationResult, error) {
	e.preflightOnce.Do(func() { e.reportUnpollableThresholds(ctx) })

	result := EvaluationResult{ByType: map[string]int{}}
	var firstErr error

	for _, threshold := range e.thresholds() {
		if err := threshold(ctx, &result); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			if e.Log != nil {
				e.Log.ErrorContext(ctx, "alerting: threshold evaluation failed", "error", err)
			}
		}
	}
	return result, firstErr
}

type thresholdPass func(context.Context, *EvaluationResult) error

func (e *Evaluator) thresholds() []thresholdPass {
	return []thresholdPass{
		e.structureFuel,
		e.starbaseFuel,
		e.inactiveMembers,
		e.expiringContracts,
	}
}

// routedOrSkip resolves one alert type's routing ONCE per pass, before any
// row is read.
//
// This is not merely an optimisation. Emit resolves routing itself, inside
// the transaction it opens per occurrence, so evaluating a threshold with
// forty crossed subjects and no subscribers would open forty transactions
// to discover forty times that nobody is listening. Asking once, first,
// makes an unrouted threshold cost one query — which matters because
// `default_enabled = false` types like corporation.member.inactive are
// expected to be unrouted on most installations, forever.
func (e *Evaluator) routedOrSkip(ctx context.Context, alertType string) (bool, error) {
	routing, err := Resolve(ctx, store.New(e.Pool), alertType)
	if err != nil {
		return false, err
	}
	return !routing.IsEmpty(), nil
}

// corporationScoped builds the TargetFilter for a subject owned by one
// corporation — see EmitRequest.TargetFilter for why a threshold needs one
// and a notification does not.
func corporationScoped(corporationID int64) func(Target) bool {
	ref := strconv.FormatInt(corporationID, 10)
	return func(t Target) bool {
		if t.Kind != "corporation" {
			return true
		}
		return t.Ref == ref
	}
}

// emitThreshold is the shared tail of all four passes: one subject, one
// fingerprint, one Emit.
func (e *Evaluator) emitThreshold(
	ctx context.Context, result *EvaluationResult,
	alertType, subjectKind string, subjectID, corporationID int64,
	bucket string, occurredAt time.Time, payload map[string]any,
) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("alerting: encoding %s payload for %s %d: %w", alertType, subjectKind, subjectID, err)
	}

	emitted, err := e.Emitter.Emit(ctx, EmitRequest{
		AlertType:  alertType,
		Payload:    encoded,
		OccurredAt: occurredAt,
		Fingerprint: func(t Target) Fingerprint {
			return ThresholdFingerprint(alertType, subjectKind, subjectID, bucket, t)
		},
		TargetFilter: corporationScoped(corporationID),
		// No Register: a threshold type's app.alert_type row carries a NOT
		// NULL source_route_id that only the seed can supply (the
		// threshold_declares_source CHECK enforces it), so a missing row
		// is a seeding problem and must surface as one rather than being
		// papered over with a registration that would violate the
		// constraint. See Emitter.IngestNotification's own note.
	})
	if err != nil {
		return err
	}
	result.Subjects++
	result.ByType[alertType]++
	result.Emitted += emitted.EventsRecorded
	result.Deduplicated += emitted.EventsDeduplicated
	return nil
}

func (e *Evaluator) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

// ── the four thresholds ──────────────────────────────────────────────────

const (
	alertStructureFuelLow = "corporation.structure.fuel_low"
	alertStarbaseFuelLow  = "corporation.starbase.fuel_low"
	alertMemberInactive   = "corporation.member.inactive"
	alertContractExpiring = "corporation.contract.expiring"
	subjectKindStructure  = "structure"
	subjectKindStarbase   = "starbase"
	subjectKindCharacter  = "character"
	subjectKindContract   = "contract"
)

// structureFuel: corporation.structure.fuel_low.
//
// The re-arm token is fuel_expires itself, which is the cleanest possible
// one: it moves when — and only when — somebody refuels.
func (e *Evaluator) structureFuel(ctx context.Context, result *EvaluationResult) error {
	routed, err := e.routedOrSkip(ctx, alertStructureFuelLow)
	if err != nil {
		return err
	}
	if !routed {
		result.Unrouted++
		return nil
	}

	rows, err := store.New(e.Pool).ListStructuresLowOnFuel(ctx, e.Policy.structureFuelWithin())
	if err != nil {
		return fmt.Errorf("alerting: listing structures low on fuel: %w", err)
	}
	for _, row := range rows {
		expires := time.Time{}
		if row.FuelExpires != nil {
			expires = *row.FuelExpires
		}
		payload := map[string]any{
			"corporation_id": row.CorporationID,
			"structure_id":   row.StructureID,
			"type_id":        row.TypeID,
			"system_id":      row.SystemID,
			"fuel_expires":   expires.UTC().Format(time.RFC3339),
			"hours_remaining": int64(
				e.now().Sub(expires) / -time.Hour, // negative when already expired
			),
		}
		if row.State != nil {
			payload["state"] = *row.State
		}
		// OccurredAt is the EXPIRY, not the evaluation time: it is what the
		// alert is about, and using it means a burst of structures expiring
		// in the same coalescing window rolls up into one message even
		// though the pass that found them ran at an arbitrary moment.
		if err := e.emitThreshold(ctx, result,
			alertStructureFuelLow, subjectKindStructure, row.StructureID, row.CorporationID,
			expires.UTC().Format(time.RFC3339), expires, payload); err != nil {
			return err
		}
	}
	return nil
}

// starbaseFuel: corporation.starbase.fuel_low. The banded re-arm token is
// explained on ThresholdPolicy.StarbaseFuelBand.
func (e *Evaluator) starbaseFuel(ctx context.Context, result *EvaluationResult) error {
	routed, err := e.routedOrSkip(ctx, alertStarbaseFuelLow)
	if err != nil {
		return err
	}
	if !routed {
		result.Unrouted++
		return nil
	}

	rows, err := store.New(e.Pool).ListStarbasesLowOnFuel(ctx, e.Policy.starbaseFuelBelow())
	if err != nil {
		return fmt.Errorf("alerting: listing starbases low on fuel: %w", err)
	}
	band := e.Policy.starbaseFuelBand()
	now := e.now()
	for _, row := range rows {
		payload := map[string]any{
			"corporation_id": row.CorporationID,
			"starbase_id":    row.StarbaseID,
			"system_id":      row.SystemID,
			"fuel_quantity":  row.FuelQuantity,
			"threshold":      e.Policy.starbaseFuelBelow(),
		}
		if row.State != nil {
			payload["state"] = *row.State
		}
		bucket := "band:" + strconv.FormatInt(row.FuelQuantity/band, 10)
		if err := e.emitThreshold(ctx, result,
			alertStarbaseFuelLow, subjectKindStarbase, row.StarbaseID, row.CorporationID,
			bucket, now, payload); err != nil {
			return err
		}
	}
	return nil
}

// inactiveMembers: corporation.member.inactive, re-armed by logoff_date.
func (e *Evaluator) inactiveMembers(ctx context.Context, result *EvaluationResult) error {
	routed, err := e.routedOrSkip(ctx, alertMemberInactive)
	if err != nil {
		return err
	}
	if !routed {
		result.Unrouted++
		return nil
	}

	rows, err := store.New(e.Pool).ListInactiveCorporationMembers(ctx, e.Policy.memberInactiveFor())
	if err != nil {
		return fmt.Errorf("alerting: listing inactive corporation members: %w", err)
	}
	now := e.now()
	for _, row := range rows {
		lastOff := time.Time{}
		if row.LogoffDate != nil {
			lastOff = *row.LogoffDate
		}
		payload := map[string]any{
			"corporation_id": row.CorporationID,
			"character_id":   row.CharacterID,
			"logoff_date":    lastOff.UTC().Format(time.RFC3339),
			"inactive_days":  int64(now.Sub(lastOff).Hours() / 24),
			"threshold_days": int64(e.Policy.memberInactiveFor().Hours() / 24),
		}
		if row.LogonDate != nil {
			payload["logon_date"] = row.LogonDate.UTC().Format(time.RFC3339)
		}
		// The evaluation time is the occurrence here, deliberately: unlike
		// a fuel expiry, the moment somebody BECAME inactive is months in
		// the past and coalescing on it would put every inactive member in
		// a different window, defeating the roll-up this alert most needs.
		if err := e.emitThreshold(ctx, result,
			alertMemberInactive, subjectKindCharacter, row.CharacterID, row.CorporationID,
			lastOff.UTC().Format(time.RFC3339), now, payload); err != nil {
			return err
		}
	}
	return nil
}

// expiringContracts: corporation.contract.expiring, re-armed by
// date_expired.
func (e *Evaluator) expiringContracts(ctx context.Context, result *EvaluationResult) error {
	routed, err := e.routedOrSkip(ctx, alertContractExpiring)
	if err != nil {
		return err
	}
	if !routed {
		result.Unrouted++
		return nil
	}

	rows, err := store.New(e.Pool).ListExpiringCorporationContracts(ctx, e.Policy.contractExpiringWithin())
	if err != nil {
		return fmt.Errorf("alerting: listing expiring corporation contracts: %w", err)
	}
	for _, row := range rows {
		payload := map[string]any{
			"corporation_id": row.CorporationID,
			"contract_id":    row.ContractID,
			"type":           row.Type,
			"status":         row.Status,
			"date_expired":   row.DateExpired.UTC().Format(time.RFC3339),
		}
		if row.Title != nil {
			payload["title"] = *row.Title
		}
		if err := e.emitThreshold(ctx, result,
			alertContractExpiring, subjectKindContract, row.ContractID, row.CorporationID,
			row.DateExpired.UTC().Format(time.RFC3339), row.DateExpired, payload); err != nil {
			return err
		}
	}
	return nil
}
