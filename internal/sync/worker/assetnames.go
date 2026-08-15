package worker

// ── PHASE 20.5, DEFECT B30: handlers.ParseAssetNames ─────────────────────
//
// The one member of B30's thirteen that is NOT a fan-out and NOT
// subscribable. POST /{owner}/{id}/assets/names takes a request body — the
// item ids the assets LIST call just returned — and internal/sync/subscribe's
// reconciliation is `method = 'GET'`, correctly: a subscription is a polling
// schedule, and there is nothing to poll a POST for. It is not "correctly
// unreachable" either, because app.asset.name is a real column, GET
// /api/v1/{owner}/{id}/assets serves it, and it was NULL on every row of
// every installation.
//
// So it is wired as an ENRICHMENT of the assets sync: the same subscription,
// the same sync_run, a second upstream call whose input is the first call's
// output. That is the shape ESI's API forces — there is nothing to ask names
// for until the list has been read — and it is why this is not simply a
// fourteenth dispatch entry.
//
// Making it possible needed one gateway change: internal/esi.Request had no
// Body field, so HANGAR's gateway was structurally GET-only. See
// Request.Body's own comment for why a request with a body is never cached.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/hangar-project/hangar/internal/esi"
	"github.com/hangar-project/hangar/internal/store"
	"github.com/hangar-project/hangar/internal/store/gen"
	"github.com/hangar-project/hangar/internal/sync"
	"github.com/hangar-project/hangar/internal/sync/handlers"
)

// ── THE CORPORATION BINDING, AND WHY IT ARRIVED A PHASE LATE (B47) ───────
// Phase 20.5 wired the character half only, and recorded why the
// corporation half was deliberately left out: there was nothing to enrich.
// GET /corporations/{corporation_id}/assets was absent from
// corporationDispatch entirely, so a corporation's assets had never synced
// on any installation and app.asset held no corporation-owned rows to name.
// That omission was the symptom; the missing dispatch entry was the defect,
// and it is B47.
//
// Phase 20.6 wires the list route (see corporationAssetsPath in
// corporation.go), and this enrichment follows it in the same commit — the
// "for free" that 20.5 predicted. Both owners now take the identical path
// through syncAssetNames, which is why the function was written
// owner-generic in the first place.
const (
	characterAssetNamesPath = "/characters/{character_id}/assets/names"

	// assetNamesBatchSize is the spec's own maximum for the request body
	// ("maxItems: 1000" on the ids array). A larger batch is a 400 from ESI,
	// so this is a limit, not a tuning knob.
	assetNamesBatchSize = 1000

	// unnamedAssetSentinel is what ESI returns as the `name` of an item that
	// has none. Measured, not assumed — see the skip below.
	unnamedAssetSentinel = "None"
)

// syncAssetNames fetches and applies the names of every NAMEABLE asset in
// the page set the caller just synced.
//
// Only singletons are asked about. ESI returns a name for a singleton
// container or ship and nothing for a stack of ammunition, so filtering here
// is not an optimisation — it is the difference between one request per
// thousand items and one request per thousand NAMEABLE items, which on a
// real corporation hangar is two orders of magnitude.
//
// EVERY FAILURE DEGRADES. A missing catalogue row, a blocked-by-pin route, a
// 403 (the token has assets scope but the character cannot read this
// corporation's), a 404, a refusal from either governor: all return the rows
// applied so far and no error. The assets themselves have already been
// committed by the caller's own handler and are correct without names; a
// name is an adornment, and failing the whole assets sync because an
// adornment could not be fetched would trade real data for cosmetic data.
// The one exception is a store write failure, which is returned, because
// that says something is wrong with the database rather than with ESI.
func syncAssetNames(
	ctx context.Context, gw *esi.Client, s *store.Store,
	namesPath, ownerParam, ownerKind string, ownerID, actingCharacterID int64,
	accessToken string, assetsBody []byte,
) (int32, error) {
	assets, err := handlers.ParseAssets(assetsBody)
	if err != nil {
		// The caller's own handler has already parsed this body successfully,
		// so a failure here is impossible rather than merely unlikely —
		// reported as no-work rather than swallowed silently.
		return 0, nil
	}
	ids := make([]int64, 0, len(assets))
	for _, a := range assets {
		if a.IsSingleton {
			ids = append(ids, a.ItemID)
		}
	}
	if len(ids) == 0 {
		return 0, nil
	}

	route, err := s.GetEsiRouteByMethodAndPath(ctx, http.MethodPost, namesPath)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// The catalogue does not carry this route on this installation
			// (an old ingest, or CCP removed it). Not an error: Principle 5
			// says the path comes from the catalogue, and a path the
			// catalogue does not have is a path HANGAR does not call.
			return 0, nil
		}
		return 0, fmt.Errorf("worker: reading the asset-names route %s: %w", namesPath, err)
	}
	if route.BlockedByPin || route.RetiredAt != nil {
		return 0, nil
	}

	var applied int32
	for start := 0; start < len(ids); start += assetNamesBatchSize {
		end := min(start+assetNamesBatchSize, len(ids))
		body, err := json.Marshal(ids[start:end])
		if err != nil {
			return applied, fmt.Errorf("worker: encoding asset-name ids for %s %d: %w", ownerKind, ownerID, err)
		}

		resp, doErr := gw.Do(ctx, esi.Request{
			Method: route.Method, UpstreamPath: route.UpstreamPath,
			PathParams:            map[string]string{ownerParam: strconv.FormatInt(ownerID, 10)},
			Body:                  body,
			AccessToken:           accessToken,
			CacheMode:             derefStr(route.CacheMode),
			RateLimitGroup:        derefStr(route.RateLimitGroup),
			RateLimitMax:          RouteRateLimitMax(derefInt32(route.RateLimitMax)),
			RateLimitAdmissionMax: BackgroundRateLimitMax(derefStr(route.RateLimitGroup), derefInt32(route.RateLimitMax)),
			RateLimitWindow:       sync.IntervalToDuration(route.RateLimitWindow),
			UserKey:               fmt.Sprintf("hangar:%d", actingCharacterID),
			EntityID:              ownerID,
		})
		if doErr != nil {
			return applied, nil // degrade — see the doc comment
		}
		if resp.StatusCode != http.StatusOK {
			return applied, nil
		}

		names, perr := handlers.ParseAssetNames(resp.Body)
		if perr != nil {
			return applied, nil
		}
		for _, n := range names {
			// ── MEASURED AGAINST REAL ESI, PHASE 20.5 ────────────────────
			// ESI returns an element for EVERY id asked about, including the
			// ones with no name, and the name it gives them is the literal
			// four-character string "None" — not an empty string, and not an
			// omitted element. Observed on the live installation the first
			// time this ran: four singleton items, one genuinely named
			// ("Ibis - CEODude"), three modules that came back "None" and
			// were written to app.asset.name as if that were their name.
			//
			// That is precisely the failure mode this phase exists to
			// refuse: not a blank, which looks wrong, but a plausible string
			// that renders as a name and is not one.
			//
			// A player CAN name a container "None". Losing that one case is
			// the right trade: it renders identically either way, whereas
			// storing CCP's sentinel puts a false name on every unnamed
			// module in every hangar. Recorded rather than left as a
			// silently-skipped value.
			if n.Name == "" || n.Name == unnamedAssetSentinel {
				continue
			}
			rows, uerr := s.SetAssetName(ctx, gen.SetAssetNameParams{
				OwnerKind: ownerKind, OwnerID: ownerID, ItemID: n.ItemID, Name: &n.Name,
			})
			if uerr != nil {
				return applied, fmt.Errorf("worker: naming asset %d of %s %d: %w", n.ItemID, ownerKind, ownerID, uerr)
			}
			applied += int32(rows)
		}
	}
	return applied, nil
}
