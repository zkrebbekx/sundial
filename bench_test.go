package sundial

import (
	"testing"
	"time"
)

var (
	benchDur  time.Duration
	benchTime time.Time
)

// benchLoc loads a real zone once for the benchmarks, falling back to UTC if the
// zone database is unavailable so benchmarks still run.
func benchLoc() *time.Location {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return time.UTC
	}
	return loc
}

// BenchmarkWorkingMultiWeek measures Working over a span of several weeks, the
// per-day integration walking many windows, holidays and weekends.
func BenchmarkWorkingMultiWeek(b *testing.B) {
	loc := benchLoc()
	s := Schedule{
		Loc: loc,
		Week: Weekdays(
			Window{Open: At(9, 0, 0), Close: At(12, 0, 0)},
			Window{Open: At(13, 0, 0), Close: At(17, 0, 0)},
		),
		Holidays: Holidays(On(2026, 1, 1), On(2026, 1, 19)),
	}
	from := time.Date(2026, 1, 1, 8, 0, 0, 0, loc)
	to := time.Date(2026, 2, 15, 15, 0, 0, 0, loc) // ~6 weeks
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchDur = s.Working(from, to)
	}
}

// BenchmarkAddLargeBudget measures Add walking a large working-time budget
// (about a working year) forward across many windows.
func BenchmarkAddLargeBudget(b *testing.B) {
	loc := benchLoc()
	s := Schedule{Loc: loc, Week: Weekdays(Window{Open: At(9, 0, 0), Close: At(17, 0, 0)})}
	start := time.Date(2026, 1, 1, 9, 0, 0, 0, loc)
	budget := 2000 * time.Hour // ~250 working days
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchTime = s.Add(start, budget)
	}
}

// BenchmarkFired measures a poll of Fired with a completed pause, exercising the
// firing-instant fixpoint.
func BenchmarkFired(b *testing.B) {
	loc := benchLoc()
	cfg := Config{
		Schedule: Schedule{Loc: loc, Week: Weekdays(Window{Open: At(9, 0, 0), Close: At(17, 0, 0)})},
		Budget:   8 * time.Hour,
		Levels:   []Level{{"warn", 4 * time.Hour}, {"breach", 8 * time.Hour}},
	}
	start := time.Date(2026, 1, 5, 9, 0, 0, 0, loc)
	tm, err := Start(cfg, start)
	if err != nil {
		b.Fatalf("Start: %v", err)
	}
	_ = tm.Pause(start.Add(90 * time.Minute))
	_ = tm.Resume(start.Add(3 * time.Hour))
	now := start.Add(72 * time.Hour)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = tm.Fired(now)
	}
}
