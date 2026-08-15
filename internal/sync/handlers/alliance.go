// Alliance sync — Appendix A capability #37.
//
// ── PHASE 20.8: THE TABLE WHERE EVERY NAME WAS THE EMPTY STRING ──────────
// app.alliance's only writer was UpsertAllianceStub, which inserts
// an alliance id with an empty name. Two callers — the character sheet sync and the
// corporation sheet sync — create a stub so their own foreign key resolves,
// and nothing has ever filled it in. So GET /api/v1/alliances served a list
// of blanks, GET /api/v1/alliances/{id} returned a row whose only true field
// was its id, and the alliance column of every screen that joins it was
// empty on every installation ever deployed. That is capability #37, and it
// is the oldest of B48's unreachable rows.
//
// The four routes below are its whole delivery. Three of them have an
// obvious write target; the fourth does not, and says so.
//
// ── THE MEMBER-CORPORATION ROUTE WRITES NO NEW ROWS, ON PURPOSE ──────────
// /alliances/{alliance_id}/corporations returns member corporation IDS and
// nothing else. HANGAR has no app.alliance_corporation table and must not
// grow one by stubbing: a large alliance has several hundred members, and
// id-with-an-empty-name corporation rows nothing resolves would rebuild the
// defect this file exists to fix, one table across. What the route CAN
// legitimately write is the membership of corporations HANGAR already holds,
// in both directions — joined and left. See SyncAllianceCorporations.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// ownerKindAlliance is app.contact/contact_label's third owner_kind. The
// column has carried it since Phase 1b (00019_domain_social.sql names all
// three) and GET /api/v1/alliances/{id}/contacts has read it since Phase 15;
// until this phase nothing wrote it.
const ownerKindAlliance = "alliance"

// ---- GET /alliances/{alliance_id} ----

// AllianceSheetDTO is ESI's AllianceDetail schema.
//
// The response carries no alliance_id of its own — the id is only ever the
// one in the request path — so SyncAllianceSheet takes it from the caller.
// ticker, creator_id, creator_corporation_id and date_founded are marked
// required by the spec but are stored in nullable columns, so they are
// declared as the spec declares them and passed through; faction_id and
// executor_corporation_id are genuinely optional (an alliance with no
// executor is a real state — it is what an alliance in the process of
// closing looks like).
type AllianceSheetDTO struct {
	Name                  string    `json:"name"`
	Ticker                string    `json:"ticker"`
	CreatorID             int64     `json:"creator_id"`
	CreatorCorporationID  int64     `json:"creator_corporation_id"`
	ExecutorCorporationID *int64    `json:"executor_corporation_id,omitempty"`
	DateFounded           time.Time `json:"date_founded"`
	FactionID             *int32    `json:"faction_id,omitempty"`
}

func ParseAllianceSheet(body []byte) (AllianceSheetDTO, error) {
	var dto AllianceSheetDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return AllianceSheetDTO{}, fmt.Errorf("handlers: parsing alliance sheet: %w", err)
	}
	return dto, nil
}

// SyncAllianceSheet is the write that gives an alliance a name.
//
// UpsertAlliance is an INSERT ... ON CONFLICT DO UPDATE, so it both fills in
// a stub the identity syncs created and creates the row outright if the
// alliance is being resolved before anything referenced it. The
// IS DISTINCT FROM guard means a re-sync of an unchanged alliance touches no
// updated_at.
//
// ── THE EXECUTOR FOREIGN KEY IS NOT WRITTEN HERE ─────────────────────────
// app.alliance.executor_corporation_id has a FOREIGN KEY to
// app.corporation, and the executor is very often a corporation HANGAR does
// not track — writing the id would fail the constraint and abort the sync
// for a field nothing displays as more than an id. It is passed only when
// the corporation is already known; otherwise the column stays NULL, which
// is the same answer HANGAR would give anyway and does not take the whole
// alliance sheet down with it.
func SyncAllianceSheet(ctx context.Context, s *store.Store, allianceID int64, dto AllianceSheetDTO) (SyncResult, error) {
	ticker := dto.Ticker
	creatorID, creatorCorporationID := dto.CreatorID, dto.CreatorCorporationID
	founded := nilIfZero(dto.DateFounded)

	executor := dto.ExecutorCorporationID
	if executor != nil {
		if _, err := s.GetCorporation(ctx, *executor); err != nil {
			executor = nil
		}
	}

	if _, err := s.UpsertAlliance(ctx, gen.UpsertAllianceParams{
		AllianceID: allianceID, Name: dto.Name, Ticker: &ticker,
		CreatorID: &creatorID, CreatorCorporationID: &creatorCorporationID,
		ExecutorCorporationID: executor, DateFounded: founded, FactionID: dto.FactionID,
	}); ignoreUnchanged(err) != nil {
		return SyncResult{}, fmt.Errorf("handlers: upserting alliance %d: %w", allianceID, err)
	}
	return SyncResult{RowsAffected: 1}, nil
}

// ---- GET /alliances/{alliance_id}/contacts ----

// AllianceContactDTO is ESI's alliance-contacts element.
//
// It has neither is_blocked nor is_watched: those are personal flags on a
// CHARACTER's contact list, and the alliance schema declares neither
// property at all — not "omitted when false". Both columns therefore stay
// NULL for an alliance-owned contact, exactly as they do for a
// corporation-owned one (corporation_social.go records the same).
type AllianceContactDTO struct {
	ContactID   int64   `json:"contact_id"`
	ContactType string  `json:"contact_type"`
	LabelIDs    []int64 `json:"label_ids,omitempty"`
	Standing    float64 `json:"standing"`
}

func ParseAllianceContacts(body []byte) ([]AllianceContactDTO, error) {
	var dto []AllianceContactDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing alliance contacts: %w", err)
	}
	return dto, nil
}

// SyncAllianceContacts is a full-state list sync: upsert what ESI returned,
// then prune what it did not — the same shape as the character and
// corporation halves, against the same two owner-generic store queries.
func SyncAllianceContacts(ctx context.Context, s *store.Store, allianceID int64, contacts []AllianceContactDTO) (SyncResult, error) {
	ids := make([]int64, len(contacts))
	for i, c := range contacts {
		ids[i] = c.ContactID
		labelIDs := c.LabelIDs
		if labelIDs == nil {
			labelIDs = []int64{}
		}
		if _, err := s.UpsertContact(ctx, gen.UpsertContactParams{
			OwnerKind: ownerKindAlliance, OwnerID: allianceID, ContactID: c.ContactID,
			ContactType: c.ContactType, Standing: c.Standing, IsBlocked: nil, IsWatched: nil, LabelIds: labelIDs,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting contact %d for alliance %d: %w", c.ContactID, allianceID, err)
		}
	}
	if err := s.DeleteContactsNotIn(ctx, ownerKindAlliance, allianceID, ids); err != nil {
		return SyncResult{}, fmt.Errorf("handlers: pruning stale contacts for alliance %d: %w", allianceID, err)
	}
	return SyncResult{RowsAffected: int32(len(contacts))}, nil
}

// ---- GET /alliances/{alliance_id}/contacts/labels ----

type AllianceContactLabelDTO struct {
	LabelID   int64  `json:"label_id"`
	LabelName string `json:"label_name"`
}

func ParseAllianceContactLabels(body []byte) ([]AllianceContactLabelDTO, error) {
	var dto []AllianceContactLabelDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing alliance contact labels: %w", err)
	}
	return dto, nil
}

func SyncAllianceContactLabels(ctx context.Context, s *store.Store, allianceID int64, labels []AllianceContactLabelDTO) (SyncResult, error) {
	ids := make([]int64, len(labels))
	for i, l := range labels {
		ids[i] = l.LabelID
		if _, err := s.UpsertContactLabel(ctx, gen.UpsertContactLabelParams{
			OwnerKind: ownerKindAlliance, OwnerID: allianceID, LabelID: l.LabelID, Name: l.LabelName,
		}); ignoreUnchanged(err) != nil {
			return SyncResult{}, fmt.Errorf("handlers: upserting contact label %d for alliance %d: %w", l.LabelID, allianceID, err)
		}
	}
	if err := s.DeleteContactLabelsNotIn(ctx, ownerKindAlliance, allianceID, ids); err != nil {
		return SyncResult{}, fmt.Errorf("handlers: pruning stale contact labels for alliance %d: %w", allianceID, err)
	}
	return SyncResult{RowsAffected: int32(len(labels))}, nil
}

// ---- GET /alliances/{alliance_id}/corporations ----

// ParseAllianceCorporations parses the bare array of member corporation ids.
func ParseAllianceCorporations(body []byte) ([]int64, error) {
	var dto []int64
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing alliance member corporations: %w", err)
	}
	return dto, nil
}

// SyncAllianceCorporations reconciles the alliance_id of corporations HANGAR
// ALREADY HAS rows for, in both directions, and inserts nothing.
//
// ── WHY RowsAffected IS THE NUMBER OF CORRECTIONS, NOT THE MEMBER COUNT ──
// Every other list sync in this package reports how many rows the response
// described, because that is how many rows it wrote. This one writes only
// where HANGAR's record disagreed with the alliance's, so reporting the
// member count would claim several hundred writes for a pass that made none.
// A steady-state 0 here means "HANGAR's membership records already agree
// with ESI", and that is the honest reading of the number — as distinct from
// the 0 this capability produced for its whole life, which meant "nothing
// ever ran".
func SyncAllianceCorporations(ctx context.Context, s *store.Store, allianceID int64, memberIDs []int64) (SyncResult, error) {
	if memberIDs == nil {
		memberIDs = []int64{}
	}
	joined, err := s.SetCorporationAllianceMembership(ctx, allianceID, memberIDs)
	if err != nil {
		return SyncResult{}, fmt.Errorf("handlers: recording alliance %d membership: %w", allianceID, err)
	}
	left, err := s.ClearCorporationAllianceMembershipNotIn(ctx, allianceID, memberIDs)
	if err != nil {
		return SyncResult{}, fmt.Errorf("handlers: clearing departed members of alliance %d: %w", allianceID, err)
	}
	return SyncResult{RowsAffected: int32(joined + left)}, nil
}
