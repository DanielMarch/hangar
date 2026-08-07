// Package catalogue ingests ESI's OpenAPI 3.1 specification into
// app.esi_route (02_DATABASE_SCHEMA.md §4.3, 01_ARCHITECTURE.md §5).
package catalogue

import "time"

// RolloverUTCHour is the hour, UTC, at which ESI's compatibility-date "day"
// rolls over (01_ARCHITECTURE.md §5.1). All "which date is current"
// comparisons go through CurrentDate/ClampToToday — never inlined at call
// sites (roadmap Phase 2 edge case: "one function, table-tested").
const RolloverUTCHour = 11

// CurrentDate returns "today" per ESI's compatibility-date rollover rule,
// as a UTC midnight time.Time: now shifted back by the rollover offset,
// then truncated to a whole day. time.Time.Truncate rounds down to a
// multiple of the duration since the Go zero time (year 1, 00:00:00 UTC),
// which for exactly 24h aligns to a UTC day boundary — this is the same
// formula 01_ARCHITECTURE.md §5.1 gives verbatim:
// now().UTC().Add(-11h).Truncate(24h).
func CurrentDate(now time.Time) time.Time {
	return now.UTC().Add(-RolloverUTCHour * time.Hour).Truncate(24 * time.Hour)
}

// ClampToToday returns d if it is not after CurrentDate(now), else
// CurrentDate(now). ESI rejects future compatibility dates outright
// (01_ARCHITECTURE.md §5.1 edge case), so D_max — the newest date
// /meta/compatibility-dates reports — must never exceed what ESI itself
// would consider "today" at the moment of the request.
func ClampToToday(d, now time.Time) time.Time {
	today := CurrentDate(now)
	if d.After(today) {
		return today
	}
	return d
}

// dateLayout is the wire format for every compatibility date: ESI's own
// /meta/compatibility-dates entries, the X-Compatibility-Date header, and
// app.esi_route.compatibility_date / app.setting's stored pin.
const dateLayout = "2006-01-02"

// ParseDate parses a "YYYY-MM-DD" compatibility date as UTC midnight.
func ParseDate(s string) (time.Time, error) {
	return time.ParseInLocation(dateLayout, s, time.UTC)
}

// FormatDate renders a compatibility date in ESI's wire format.
func FormatDate(t time.Time) string {
	return t.UTC().Format(dateLayout)
}

// MaxDate returns the latest of a set of "YYYY-MM-DD" date strings, parsed
// as UTC midnight. Used on /meta/compatibility-dates' response list to
// compute D_max (01_ARCHITECTURE.md §5.1: "D_max = newest entry").
func MaxDate(dates []string) (time.Time, error) {
	var max time.Time
	for i, s := range dates {
		d, err := ParseDate(s)
		if err != nil {
			return time.Time{}, err
		}
		if i == 0 || d.After(max) {
			max = d
		}
	}
	return max, nil
}
