package v2shim

import (
	"sort"

	"github.com/hangar-project/hangar/internal/domain"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// translate_market.go — the two market-order routes, and the first place on
// this surface where HANGAR money reaches the wire.
//
// ── WHY THESE TWO WERE THE ONES TO WRITE (PHASE 20.9, DEFECT B55) ────────
// Thirteen routes shared the blocker `reasonNoKeysetWindow`, whose text said
// each "needs a store query that can return the full ordered set for a
// window ... not yet written". For these two the claim was FALSE and had
// been since Phase 1b:
//
//	-- name: ListMarketOrdersByOwner :many
//	SELECT * FROM app.market_order WHERE owner_kind = $1 AND owner_id = $2 ORDER BY issued DESC;
//
// No LIMIT, no keyset, no OFFSET — the whole ordered relation, and already
// called in production by /api/v1 (internal/api/v1/alliances_market.go).
// The blocker was a statement about the store that nobody had checked
// against the store; see classification.go's note on B55 for the other
// eight routes it was equally wrong about.
//
// They were picked first because they are the ONLY money-bearing route in
// the pending set that is not also blocked on something permanent: `price`
// and `escrow` are both NUMERIC(30,2), so Money/MoneyOrNull/numericString
// stop being unreachable helpers and start being the thing that produces
// the bytes. (MoneyOrZero remains unreachable — see money.go.)
//
// ── WHAT THE RECORDING PINS, AND WHAT IT DOES NOT ────────────────────────
// Both recordings hold exactly ONE row. That pins every field name, every
// field ORDER, every type and every formatting rule — and it cannot pin the
// ROW order, because one row has only one arrangement. The shim sorts by
// order_id, which is the argument characterCorporationHistory already made
// and the corporation-history recording actually measured: legacy's
// unordered `->paginate()` returns an InnoDB clustered-index scan, which is
// primary-key order, and that recording shows record_id 1 before record_id 2
// even though record 1 is the NEWER row. So the rule is evidenced; its
// application HERE is inference, and it is written down as inference.
//
// ── `state` IS THE EMPTY STRING, AND THAT IS NOT A PLACEHOLDER ───────────
// fixtures.php writes `'state' => 'open'` and the recording emits
// `"state":""`. Legacy's column did not retain the value. HANGAR has no
// column to retain it with: app.market_order has no `state`, and
// app.market_order_history does — which mirrors the live ESI spec exactly,
// where `/characters/{id}/orders` and `/corporations/{id}/orders` carry no
// `state` property at all and only the `/orders/history` routes do
// (measured against the ingested catalogue, compatibility date 2026-08-04).
// So an open order has no state in either system, legacy renders that as
// `""`, and the shim emits `""`. It is a constant because the fact is
// constant, not because the value was unavailable.

// legacyOpenOrderState is what legacy put in an open order's `state`. See
// the file comment: the open-orders ESI routes carry no `state`, so this is
// the only value the field ever had on this route.
const legacyOpenOrderState = ""

// legacyTypeObject is legacy's eager-loaded `type` relation as it appears in
// every recording: Laravel's `belongsTo(InvType::class)->withDefault(...)`
// against an EMPTY `invTypes` table, which yields the foreign key plus the
// default name and nothing else.
//
// The SDE is empty by construction in the corpus — sde.php creates the
// lookup tables and leaves them so, because a POPULATED legacy SDE embeds
// the whole Fuzzwork `invTypes` row (portionSize, basePrice, groupID, …) and
// HANGAR's `sde.*` comes from CCP's modern JSONL export and cannot reproduce
// that column vocabulary. That residual gap is docs/APPENDIX_C_MIGRATION.md
// line 303, written down rather than papered over, and this function is the
// half the shim CAN match exactly.
//
// Note what this is NOT: it is not a lookup that failed. HANGAR does not
// consult its own SDE here even when one has been imported, for the same
// reason entity.go declines to resolve a name it holds — the contract is
// byte-identity with the recording, and /api/v1 has always served the
// resolved value.
func legacyTypeObject(typeID int64) *Obj {
	return NewObj(2).
		Set("typeID", Int(typeID)).
		Set("typeName", legacyUnknownEntityName)
}

// characterMarketOrders — legacy's `character_orders` row, which
// CharacterController returned as a raw model rather than through a
// Resource. The field order is therefore the physical MySQL column order
// after SeAT's migrations, minus the model's `$hidden`, with the eager-loaded
// `type` relation appended last:
//
//	order_id, region_id, location_id, range, is_buy_order, price,
//	volume_total, volume_remain, issued, min_volume, duration,
//	is_corporation, escrow, state, type
//
// `type_id` does not appear: it is hidden and replaced by `type`.
func characterMarketOrders(req *Request) (any, error) {
	return marketOrders(req, string(domain.OwnerCharacter), characterMarketOrderRow)
}

// corporationMarketOrders — the same table family with a DIFFERENT field
// order and a different field set, which is why the two are not one
// function with a flag:
//
//	order_id, region_id, location_id, range, is_buy_order, price,
//	volume_total, volume_remain, issued, issued_by, min_volume,
//	wallet_division, duration, escrow, state, type
//
// `issued_by` and `wallet_division` exist only here, `is_corporation` only
// on the character side, and the two shared columns that follow them sit in
// different positions. Both are visible in the bytes.
func corporationMarketOrders(req *Request) (any, error) {
	return marketOrders(req, string(domain.OwnerCorporation), corporationMarketOrderRow)
}

func marketOrders(req *Request, ownerKind string, row func(gen.AppMarketOrder) (*Obj, error)) (any, error) {
	if len(req.IDs) == 0 {
		return nil, errBadID
	}
	ctx := req.HTTP.Context()

	orders, err := req.Deps.Store.ListMarketOrdersByOwner(ctx, ownerKind, req.IDs[0])
	if err != nil {
		return nil, internalError("listing market orders", err)
	}

	// The store orders by `issued DESC` for /api/v1's benefit; legacy's
	// unordered paginate() returned primary-key order. See the file comment
	// on what the one-row recording can and cannot establish.
	sort.SliceStable(orders, func(i, j int) bool { return orders[i].OrderID < orders[j].OrderID })

	page := Window(orders, req.Page, LegacyPerPage)
	rows := make(Arr, 0, len(page))
	for _, order := range page {
		encoded, err := row(order)
		if err != nil {
			return nil, internalError("rendering market order", err)
		}
		rows = append(rows, encoded)
	}
	return req.PageOf(rows, int64(len(orders))), nil
}

func characterMarketOrderRow(order gen.AppMarketOrder) (*Obj, error) {
	price, err := Money(order.Price)
	if err != nil {
		return nil, err
	}
	escrow, err := MoneyOrNull(order.Escrow)
	if err != nil {
		return nil, err
	}

	return NewObj(15).
		Set("order_id", Int(order.OrderID)).
		Set("region_id", Int(int64(order.RegionID))).
		Set("location_id", Int(order.LocationID)).
		Set("range", order.Range).
		Set("is_buy_order", order.IsBuyOrder).
		Set("price", price).
		Set("volume_total", Int(order.VolumeTotal)).
		Set("volume_remain", Int(order.VolumeRemain)).
		Set("issued", legacyTime(order.Issued)).
		Set("min_volume", optInt(order.MinVolume)).
		Set("duration", Int(int64(order.Duration))).
		// `is_corporation` is an INTEGER here and `is_buy_order` two fields
		// above it is a BOOLEAN, in the same object, on the same row. That is
		// not a transcription slip: SeAT's CharacterOrder casts is_buy_order
		// to bool and does not cast is_corporation, so MySQL's tinyint
		// reaches json_encode as 0/1. Both spellings are in the recording.
		Set("is_corporation", legacyBoolAsInt(order.IsCorporation)).
		Set("escrow", escrow).
		Set("state", legacyOpenOrderState).
		Set("type", legacyTypeObject(int64(order.TypeID))), nil
}

func corporationMarketOrderRow(order gen.AppMarketOrder) (*Obj, error) {
	price, err := Money(order.Price)
	if err != nil {
		return nil, err
	}
	escrow, err := MoneyOrNull(order.Escrow)
	if err != nil {
		return nil, err
	}

	var division any
	if order.WalletDivision != nil {
		division = Int(int64(*order.WalletDivision))
	}

	return NewObj(16).
		Set("order_id", Int(order.OrderID)).
		Set("region_id", Int(int64(order.RegionID))).
		Set("location_id", Int(order.LocationID)).
		Set("range", order.Range).
		Set("is_buy_order", order.IsBuyOrder).
		Set("price", price).
		Set("volume_total", Int(order.VolumeTotal)).
		Set("volume_remain", Int(order.VolumeRemain)).
		Set("issued", legacyTime(order.Issued)).
		Set("issued_by", optInt(order.IssuedBy)).
		Set("min_volume", optInt(order.MinVolume)).
		Set("wallet_division", division).
		Set("duration", Int(int64(order.Duration))).
		Set("escrow", escrow).
		Set("state", legacyOpenOrderState).
		Set("type", legacyTypeObject(int64(order.TypeID))), nil
}

// legacyBoolAsInt renders a HANGAR boolean the way an UNCAST MySQL tinyint
// reached PHP's json_encode: as 0 or 1, not as false or true.
//
// Named rather than inlined so that every place the shim deliberately emits
// a number where the value is a boolean is greppable — the distinction is
// invisible at a glance and it IS the bytes.
func legacyBoolAsInt(b bool) Int {
	if b {
		return Int(1)
	}
	return Int(0)
}
