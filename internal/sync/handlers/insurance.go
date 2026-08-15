// Insurance price sync — Appendix A capability #42.
//
// ── PHASE 20.7 (B48): A TABLE, AN ENDPOINT, AND NO WRITER ────────────────
// app.insurance_price has existed since Phase 15 and GET
// /api/v1/tools/insurance has served it since the same phase.
// UpsertInsurancePrice was generated, compiled, and called by nothing, so
// the endpoint returned `"data":[]` on every installation ever deployed —
// one of the twenty-seven writer-less mutations Phase 20.6 measured.
//
// The route is unauthenticated: no scope, no token, no owner. It is
// therefore a GLOBAL subscription (worker/global.go), the same shape as
// /markets/prices, and it is the one B48 capability guaranteed to land a
// non-empty result on any installation — EVE publishes insurance levels for
// every insurable ship type, so a zero row count here would be a real
// defect rather than an honest empty.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// InsuranceLevelDTO is one insurance tier for one ship type. `name` is
// CCP's own tier label ("Basic", "Standard", "Bronze", ... "Platinum") and
// is what app.insurance_price.level stores — the column is text precisely
// because the vocabulary is CCP's and open, not an enum HANGAR may narrow.
type InsuranceLevelDTO struct {
	Cost   float64 `json:"cost"`
	Name   string  `json:"name"`
	Payout float64 `json:"payout"`
}

// InsurancePriceDTO is one element of GET /insurance/prices: a ship type
// and every insurance level available for it.
type InsurancePriceDTO struct {
	TypeID int32               `json:"type_id"`
	Levels []InsuranceLevelDTO `json:"levels"`
}

func ParseInsurancePrices(body []byte) ([]InsurancePriceDTO, error) {
	var dto []InsurancePriceDTO
	if err := json.Unmarshal(body, &dto); err != nil {
		return nil, fmt.Errorf("handlers: parsing insurance prices: %w", err)
	}
	return dto, nil
}

// SyncInsurancePrices upserts one row per (type_id, level).
//
// ── WHY THERE IS NO PRUNE ────────────────────────────────────────────────
// Every other full-state list sync in this package deletes the rows ESI did
// not return. This one does not, and the difference is deliberate: the
// response is EVE-wide reference data, not a per-owner set. A short read —
// a partial page, a truncated body, an upstream having a bad minute — would
// delete the entire insurance table on a prune-by-absence, and the next
// endpoint call would serve an empty collection that looks exactly like the
// bug this phase is fixing. A type that stops being insurable keeps a stale
// row until it is next quoted, which is the strictly less harmful failure.
//
// RowsAffected counts LEVELS, not types: the levels are what land in the
// table, and reporting the type count would under-report the write by
// roughly the number of tiers per hull.
func SyncInsurancePrices(ctx context.Context, s *store.Store, prices []InsurancePriceDTO) (SyncResult, error) {
	var n int32
	for _, p := range prices {
		for _, l := range p.Levels {
			if _, err := s.UpsertInsurancePrice(ctx, gen.UpsertInsurancePriceParams{
				TypeID: p.TypeID,
				Level:  l.Name,
				Cost:   decimal.NewFromFloat(l.Cost),
				Payout: decimal.NewFromFloat(l.Payout),
			}); ignoreUnchanged(err) != nil {
				return SyncResult{}, fmt.Errorf("handlers: upserting insurance price for type %d level %q: %w", p.TypeID, l.Name, err)
			}
			n++
		}
	}
	return SyncResult{RowsAffected: n}, nil
}
