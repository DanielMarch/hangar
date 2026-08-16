package main

// generate.go is Gate 3's producer: it offers alert occurrences across all
// three §4.4 categories, through the seams the application really uses.
//
// ── WHY EACH CATEGORY GOES THROUGH ITS OWN SEAM ──────────────────────────
// Defect B25 was a complete delivery pipeline with nothing producing
// alerts, and it survived eighteen phases because every test constructed
// what it needed. A Gate 3 run that called Emitter.Emit for all three
// categories would reproduce that blindness exactly: it would prove the
// emitter works, which was never in doubt, and would say nothing about
// whether anything CALLS it.
//
// So:
//
//	esi_notification -> handlers.SyncCharacterNotifications, the real sync
//	                    handler, which fires NotificationObservedHook
//	threshold        -> alerting.Evaluator.Evaluate, the real evaluator,
//	                    over rows world.go seeded for it
//	domain_event     -> Emitter.Emit, which IS the real seam for this
//	                    category — a domain event has no upstream fetch
//	                    behind it, it is HANGAR's own action

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hangar-project/hangar/internal/alerting"
	"github.com/hangar-project/hangar/internal/sync/handlers"
	"github.com/hangar-project/hangar/test/load"
)

// generate runs the producer until `window` elapses.
func generate(
	ctx context.Context,
	w *world,
	emitter *alerting.Emitter,
	evaluator *alerting.Evaluator,
	tally *load.Gate3Tally,
	window, interval time.Duration,
) error {
	deadline := time.Now().Add(window)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	batch := 0
	notificationID := int64(9_000_000)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		batch++

		// ── category 1: CCP notifications through the real sync handler ────
		//
		// EVERY notification type, every batch. The first version took a
		// sliding window of 24 consecutive types advancing by one per batch,
		// which covers the catalogue eventually but not soon: after ten
		// batches it had touched types 1..35 of 49 and the run reported "7 of
		// 8 domains", missing sovereignty and alliances entirely — they sit at
		// the end of catalogue order. Whether §3's "all eight domains" clause
		// was satisfied then depended on how long the run happened to be,
		// which is not a property a gate should have.
		var notifications []handlers.CharacterNotificationDTO
		for i, alertType := range w.notificationTypes {
			notificationID++
			notifications = append(notifications, handlers.CharacterNotificationDTO{
				NotificationID: notificationID, SenderID: 1, SenderType: "corporation",
				Type: alertType, Timestamp: time.Now(),
				Text: fmt.Sprintf("structureID: %d\nsolarsystemID: 30000142\ncorpID: %d\n",
					1_000_000_000_000+int64(i), gate3CorporationID),
			})
		}

		// §3.3: CCP YAML that no strict parser accepts. It must import as
		// JSONB and the queue must not halt on it. Once per batch, so a
		// failure to survive it shows up early and often rather than once.
		notificationID++
		notifications = append(notifications, handlers.CharacterNotificationDTO{
			NotificationID: notificationID, SenderID: 1, SenderType: "corporation",
			Type: w.notificationTypes[batch%len(w.notificationTypes)], Timestamp: time.Now(),
			Text: "\tthis: is: not: valid: yaml: [\n",
		})

		// §3.2: an unrecognised type, every batch.
		notificationID++
		notifications = append(notifications, handlers.CharacterNotificationDTO{
			NotificationID: notificationID, SenderID: 1, SenderType: "corporation",
			Type: gate3UnknownType, Timestamp: time.Now(),
			Text: "someField: 1\n",
		})

		if _, err := handlers.SyncCharacterNotifications(ctx, w.store, gate3CharacterID, notifications); err != nil {
			return fmt.Errorf("gate3: the notification queue halted on a payload — §4.4 forbids exactly this: %w", err)
		}

		// Re-syncing the previous batch is §3.1's suppressed_by_dedupe path,
		// exercised for real rather than simulated: the same notification
		// ids arrive again and must produce no new events.
		if batch > 1 {
			if _, err := handlers.SyncCharacterNotifications(ctx, w.store, gate3CharacterID, notifications); err != nil {
				return fmt.Errorf("gate3: re-syncing a batch failed: %w", err)
			}
		}

		// ── category 2: thresholds through the real evaluator ──────────────
		// Fuel is decremented each pass so the re-arm token moves and the
		// threshold genuinely re-fires, rather than deduplicating forever
		// and contributing one occurrence to the whole run.
		if _, err := w.pool.Exec(ctx, `
			UPDATE app.corporation_structure
			   SET fuel_expires = fuel_expires - interval '5 minutes'
			 WHERE corporation_id = $1`, gate3CorporationID); err != nil {
			return fmt.Errorf("gate3: ageing structure fuel: %w", err)
		}
		result, err := evaluator.Evaluate(ctx)
		if err != nil {
			return fmt.Errorf("gate3: threshold evaluation failed: %w", err)
		}
		tally.Add(result.Emitted+result.Deduplicated, result.Emitted, result.Deduplicated, 0)

		// ── category 3: domain events through the emitter ──────────────────
		for i, alertType := range w.domainEventTypes {
			payload := json.RawMessage(fmt.Sprintf(
				`{"batch":%d,"index":%d,"observed_at":%q}`, batch, i, time.Now().UTC().Format(time.RFC3339Nano)))
			emitted, err := emitter.Emit(ctx, alerting.EmitRequest{
				AlertType: alertType, Payload: payload, OccurredAt: time.Now(),
				Fingerprint: func(target alerting.Target) alerting.Fingerprint {
					fields := alerting.SemanticFields(payload, "batch", "index")
					fields["target_kind"] = target.Kind
					fields["target_ref"] = target.Ref
					return alerting.Fingerprint{AlertType: alertType, Fields: fields}
				},
			})
			if err != nil {
				return fmt.Errorf("gate3: emitting domain event %s: %w", alertType, err)
			}
			tally.Add(emitted.EventsRecorded+emitted.EventsDeduplicated,
				emitted.EventsRecorded, emitted.EventsDeduplicated, emitted.DeliveriesEnqueued)
		}

		if batch%10 == 0 {
			fmt.Printf("gate3: batch %d — %d occurrences offered so far\n", batch, tally.Offered)
		}
	}
	return nil
}
