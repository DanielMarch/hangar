package catalogue

import (
	"testing"
	"time"
)

// TestCompatibilityDateRolloverAt1100UTC — table-driven across the 11:00
// UTC rollover boundary in both directions (roadmap Phase 2 edge case;
// 01_ARCHITECTURE.md §5.1).
func TestCompatibilityDateRolloverAt1100UTC(t *testing.T) {
	tests := []struct {
		name string
		now  time.Time
		want string
	}{
		{
			name: "just before rollover (10:59:59 UTC) is still yesterday",
			now:  time.Date(2026, time.August, 6, 10, 59, 59, 0, time.UTC),
			want: "2026-08-05",
		},
		{
			name: "exactly at rollover (11:00:00 UTC) is already today",
			now:  time.Date(2026, time.August, 6, 11, 0, 0, 0, time.UTC),
			want: "2026-08-06",
		},
		{
			name: "just after rollover (11:00:01 UTC) is today",
			now:  time.Date(2026, time.August, 6, 11, 0, 1, 0, time.UTC),
			want: "2026-08-06",
		},
		{
			name: "well before rollover (00:00:00 UTC) is yesterday",
			now:  time.Date(2026, time.August, 6, 0, 0, 0, 0, time.UTC),
			want: "2026-08-05",
		},
		{
			name: "well after rollover (23:59:59 UTC) is today",
			now:  time.Date(2026, time.August, 6, 23, 59, 59, 0, time.UTC),
			want: "2026-08-06",
		},
		{
			name: "a non-UTC input is normalised to UTC before comparison",
			// 09:30 in UTC+5 = 04:30 UTC, before rollover -> still the previous UTC day.
			now:  time.Date(2026, time.August, 6, 9, 30, 0, 0, time.FixedZone("UTC+5", 5*3600)),
			want: "2026-08-05",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatDate(CurrentDate(tt.now))
			if got != tt.want {
				t.Errorf("CurrentDate(%v) = %s, want %s", tt.now, got, tt.want)
			}
		})
	}
}

// TestClampToTodayNeverExceedsCurrentDate — future dates are rejected
// upstream, so D_max must be clamped to the rollover-adjusted today.
func TestClampToTodayNeverExceedsCurrentDate(t *testing.T) {
	now := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC) // today = 2026-08-06

	future, _ := ParseDate("2026-09-01")
	if got := FormatDate(ClampToToday(future, now)); got != "2026-08-06" {
		t.Errorf("a future date must clamp to today; got %s", got)
	}

	past, _ := ParseDate("2020-01-01")
	if got := FormatDate(ClampToToday(past, now)); got != "2020-01-01" {
		t.Errorf("a past date must not be altered; got %s", got)
	}

	exactlyToday, _ := ParseDate("2026-08-06")
	if got := FormatDate(ClampToToday(exactlyToday, now)); got != "2026-08-06" {
		t.Errorf("today itself must not be altered; got %s", got)
	}
}

func TestMaxDate(t *testing.T) {
	dates := []string{"2026-08-04", "2026-09-01", "2020-01-01", "2025-12-16"}
	max, err := MaxDate(dates)
	if err != nil {
		t.Fatal(err)
	}
	if got := FormatDate(max); got != "2026-09-01" {
		t.Errorf("MaxDate = %s, want 2026-09-01", got)
	}
}
