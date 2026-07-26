package sundial

import (
	"testing"
	"time"
)

// nyLoc loads America/New_York, skipping the calling test if the zone database
// is unavailable. A real zone is preferred over a synthetic one so the DST
// behaviour under test is genuine.
func nyLoc(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("America/New_York unavailable: %v", err)
	}
	return loc
}

// stdSchedule is the golden-vector schedule: Monday–Friday 09:00–17:00 in loc,
// no holidays.
func stdSchedule(loc *time.Location) Schedule {
	return Schedule{
		Loc:  loc,
		Week: Weekdays(Window{Open: At(9, 0, 0), Close: At(17, 0, 0)}),
	}
}

// tsMonday is a Monday: 2026-01-05.
var tsMonday = struct{ y, m, d int }{2026, 1, 5}

// ts builds an instant dayOff days after the base Monday, at h:m in loc, where
// dayOff 0=Mon, 1=Tue … 4=Fri, 5=Sat, 6=Sun, 7=next Mon.
func ts(loc *time.Location, dayOff, h, m int) time.Time {
	return time.Date(tsMonday.y, time.Month(tsMonday.m), tsMonday.d+dayOff, h, m, 0, 0, loc)
}
