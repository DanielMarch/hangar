package v2shim

import (
	"sort"

	"github.com/hangar-project/hangar/internal/domain"
	"github.com/hangar-project/hangar/internal/store/gen"
)

// translate_industry.go — the two industry-job routes.
//
// Unblocked by the same measurement as translate_market.go (defect B55):
// `ListIndustryJobsByOwner` returns the whole ordered relation with no LIMIT
// and no keyset, and has done since Phase 1b. The two recordings have
// IDENTICAL field order, which is worth stating because the two market-order
// recordings do not — SeAT's character and corporation industry tables were
// migrated in step and its order tables were not, and only the corpus says
// which is which.
//
// ── `station_id` IS NULL ON THE CORPORATION ROUTE, ALWAYS ────────────────
// Both recordings carry the SAME 22 keys in the same order, and the
// corporation one records `"station_id": null` where the character one
// records `60003760`. That is not missing fixture data. It is ESI:
//
//	/characters/{id}/industry/jobs    → station_id
//	/corporations/{id}/industry/jobs  → location_id
//
// (measured against the ingested catalogue, compatibility date 2026-08-04 —
// the two schemas differ in exactly that one property name and agree on the
// other 21). SeAT mirrored ESI column-for-column, so `corporation_industry_jobs`
// has a `location_id` its model HIDES and a vestigial `station_id` NO SYNC
// EVER WRITES. Every corporation industry job on every legacy installation
// therefore reports a null station and no location at all.
//
// HANGAR normalised the two names into one NOT NULL `station_id`, which is
// the better schema and is exactly why the shim has to emit null here: the
// byte-compatible answer is legacy's dead column, not HANGAR's live one. Same
// rule as entity.go — /api/v1 has the real value and always did.
//
// This is the field the first implementation got wrong, and the corpus caught
// it on the first run. A shim written from HANGAR's schema, or from
// fixtures.php, would have shipped 60003760 and looked entirely reasonable.

// characterIndustry — legacy's `character_industry_jobs` row.
func characterIndustry(req *Request) (any, error) {
	return industryJobs(req, string(domain.OwnerCharacter), true)
}

// corporationIndustry — legacy's `corporation_industry_jobs` row: same
// fields, same order, and `station_id` permanently null. See above.
func corporationIndustry(req *Request) (any, error) {
	return industryJobs(req, string(domain.OwnerCorporation), false)
}

func industryJobs(req *Request, ownerKind string, hasStationID bool) (any, error) {
	if len(req.IDs) == 0 {
		return nil, errBadID
	}
	ctx := req.HTTP.Context()

	jobs, err := req.Deps.Store.ListIndustryJobsByOwner(ctx, ownerKind, req.IDs[0])
	if err != nil {
		return nil, internalError("listing industry jobs", err)
	}

	// The store orders by start_date DESC for /api/v1; legacy's unordered
	// paginate() scanned the clustered index, which is (owner, job_id). See
	// translate_market.go on why that rule is evidenced and its application
	// to a one-row recording is inference.
	sort.SliceStable(jobs, func(i, j int) bool { return jobs[i].JobID < jobs[j].JobID })

	page := Window(jobs, req.Page, LegacyPerPage)
	rows := make(Arr, 0, len(page))
	for _, job := range page {
		encoded, err := industryJobRow(job, hasStationID)
		if err != nil {
			return nil, internalError("rendering industry job", err)
		}
		rows = append(rows, encoded)
	}
	return req.PageOf(rows, int64(len(jobs))), nil
}

func industryJobRow(job gen.AppIndustryJob, hasStationID bool) (*Obj, error) {
	// `cost` is a MySQL DOUBLE in legacy and NUMERIC(30,2) here — the same
	// lossy boundary every money field on this surface crosses, so it goes
	// through MoneyOrNull rather than through Float.
	cost, err := MoneyOrNull(job.Cost)
	if err != nil {
		return nil, err
	}

	// The key is present either way — only its value differs. Dropping it on
	// the corporation route would change the field COUNT, and the recording
	// has all 22 keys on both.
	var stationID any
	if hasStationID {
		stationID = Int(job.StationID)
	}

	return NewObj(22).
		Set("job_id", Int(job.JobID)).
		Set("installer_id", Int(job.InstallerID)).
		Set("facility_id", Int(job.FacilityID)).
		Set("station_id", stationID).
		Set("activity_id", Int(int64(job.ActivityID))).
		Set("blueprint_id", Int(job.BlueprintID)).
		Set("blueprint_location_id", Int(job.BlueprintLocationID)).
		Set("output_location_id", Int(job.OutputLocationID)).
		Set("runs", Int(int64(job.Runs))).
		Set("cost", cost).
		Set("licensed_runs", optInt32(job.LicensedRuns)).
		// `probability` is a float in BOTH schemas — a genuine double, not a
		// money value that had to be degraded to reach one.
		Set("probability", Float(job.Probability)).
		Set("status", job.Status).
		Set("duration", Int(int64(job.Duration))).
		Set("start_date", legacyTime(job.StartDate)).
		Set("end_date", legacyTime(job.EndDate)).
		Set("pause_date", legacyTimeOrNull(job.PauseDate)).
		Set("completed_date", legacyTimeOrNull(job.CompletedDate)).
		Set("completed_character_id", optInt(job.CompletedCharacterID)).
		Set("successful_runs", optInt32(job.SuccessfulRuns)).
		Set("blueprint", legacyTypeObject(int64(job.BlueprintTypeID))).
		Set("product", legacyTypeObjectOrNullID(job.ProductTypeID)), nil
}
