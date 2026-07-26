package sundial

import (
	"testing"
	"time"
)

// The nine golden vectors from docs/DESIGN.md, each as a Given/When/Then case
// asserting exact durations and instants. These pin the contract; the wider
// suites in schedule_test.go, timer_test.go and snapshot_test.go exercise the
// edges around them.

func TestGolden1BasicSpan(t *testing.T) {
	loc := nyLoc(t)
	s := stdSchedule(loc)
	t.Run("Given a Mon–Fri 09:00–17:00 schedule", func(t *testing.T) {
		t.Run("When measuring Monday 09:00 to 12:00", func(t *testing.T) {
			got := s.Working(ts(loc, 0, 9, 0), ts(loc, 0, 12, 0))
			t.Run("Then it is 3 working hours", func(t *testing.T) {
				if got != 3*time.Hour {
					t.Fatalf("Working = %v, want 3h", got)
				}
			})
		})
	})
}

func TestGolden2OvernightSkip(t *testing.T) {
	loc := nyLoc(t)
	s := stdSchedule(loc)
	t.Run("Given the standard schedule", func(t *testing.T) {
		t.Run("When measuring Monday 16:00 to Tuesday 10:00", func(t *testing.T) {
			got := s.Working(ts(loc, 0, 16, 0), ts(loc, 1, 10, 0))
			t.Run("Then the overnight gap counts as 2 working hours", func(t *testing.T) {
				if got != 2*time.Hour {
					t.Fatalf("Working = %v, want 2h", got)
				}
			})
		})
	})
}

func TestGolden3WeekendSkip(t *testing.T) {
	loc := nyLoc(t)
	s := stdSchedule(loc)
	t.Run("Given the standard schedule", func(t *testing.T) {
		t.Run("When measuring Friday 16:00 to Monday 10:00", func(t *testing.T) {
			got := s.Working(ts(loc, 4, 16, 0), ts(loc, 7, 10, 0))
			t.Run("Then the weekend is skipped and it is 2 working hours", func(t *testing.T) {
				if got != 2*time.Hour {
					t.Fatalf("Working = %v, want 2h", got)
				}
			})
		})
	})
}

func TestGolden4HolidaySkip(t *testing.T) {
	loc := nyLoc(t)
	s := stdSchedule(loc)
	// The Monday after the base week is a holiday.
	s.Holidays = Holidays(On(2026, 1, 12))
	t.Run("Given the standard schedule with the next Monday a holiday", func(t *testing.T) {
		t.Run("When measuring Friday 16:00 to the following Tuesday 10:00", func(t *testing.T) {
			got := s.Working(ts(loc, 4, 16, 0), ts(loc, 8, 10, 0))
			t.Run("Then Sat, Sun and the holiday Monday are excluded, giving 2 working hours", func(t *testing.T) {
				if got != 2*time.Hour {
					t.Fatalf("Working = %v, want 2h", got)
				}
			})
		})
	})
}

func TestGolden5AddRoundTrip(t *testing.T) {
	loc := nyLoc(t)
	s := stdSchedule(loc)
	t.Run("Given the standard schedule and a start of Friday 16:00", func(t *testing.T) {
		start := ts(loc, 4, 16, 0)
		t.Run("When advancing 8 working hours", func(t *testing.T) {
			got := s.Add(start, 8*time.Hour)
			// 1h Friday (16–17) + 7h Monday (09–16) = the next Monday 16:00.
			want := ts(loc, 7, 16, 0)
			t.Run("Then it lands at the next Monday 16:00 (not Tuesday)", func(t *testing.T) {
				if !got.Equal(want) {
					t.Fatalf("Add = %v, want %v", got, want)
				}
			})
			t.Run("Then Working from start to that instant is exactly the budget", func(t *testing.T) {
				if rt := s.Working(start, got); rt != 8*time.Hour {
					t.Fatalf("round-trip Working = %v, want 8h", rt)
				}
			})
		})
	})
}

func TestGolden6DSTSafe(t *testing.T) {
	loc := nyLoc(t)
	s := stdSchedule(loc)
	t.Run("Given a start on the Friday before US spring-forward (2025-03-09)", func(t *testing.T) {
		start := time.Date(2025, 3, 7, 16, 0, 0, 0, loc) // Friday 16:00
		t.Run("When advancing an 8 working-hour budget across the transition", func(t *testing.T) {
			got := s.Add(start, 8*time.Hour)
			want := time.Date(2025, 3, 10, 16, 0, 0, 0, loc) // Monday 16:00 EDT
			t.Run("Then it lands at Monday 16:00, the civil instant unchanged", func(t *testing.T) {
				if !got.Equal(want) {
					t.Fatalf("Add = %v, want %v", got, want)
				}
			})
			t.Run("Then the budget still round-trips to 8 working hours", func(t *testing.T) {
				if rt := s.Working(start, got); rt != 8*time.Hour {
					t.Fatalf("round-trip Working = %v, want 8h", rt)
				}
			})
			t.Run("Then the real wall-clock span is one hour shorter (71h)", func(t *testing.T) {
				if span := got.Sub(start); span != 71*time.Hour {
					t.Fatalf("wall span = %v, want 71h", span)
				}
			})
			t.Run("Then the spring-forward Monday still holds 8 working hours", func(t *testing.T) {
				md := s.Working(time.Date(2025, 3, 10, 9, 0, 0, 0, loc), time.Date(2025, 3, 10, 17, 0, 0, 0, loc))
				if md != 8*time.Hour {
					t.Fatalf("Monday working = %v, want 8h", md)
				}
			})
		})
	})
}

func TestGolden7Pause(t *testing.T) {
	loc := nyLoc(t)
	s := stdSchedule(loc)
	cfg := Config{Schedule: s, Budget: 8 * time.Hour}
	t.Run("Given an 8h timer started Monday 09:00 (due Monday 17:00)", func(t *testing.T) {
		tm, err := Start(cfg, ts(loc, 0, 9, 0))
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		if due := tm.DueAt(); !due.Equal(ts(loc, 0, 17, 0)) {
			t.Fatalf("initial DueAt = %v, want Mon 17:00", due)
		}
		t.Run("When paused Monday 11:00–14:00 (3 working hours)", func(t *testing.T) {
			mustPause(t, tm, ts(loc, 0, 11, 0))
			mustResume(t, tm, ts(loc, 0, 14, 0))
			t.Run("Then DueAt shifts later by the 3 paused working hours, to Tuesday 12:00", func(t *testing.T) {
				if due := tm.DueAt(); !due.Equal(ts(loc, 1, 12, 0)) {
					t.Fatalf("DueAt = %v, want Tue 12:00", due)
				}
			})
		})
	})

	t.Run("Given an 8h timer and a pause spanning only the weekend", func(t *testing.T) {
		tm, err := Start(cfg, ts(loc, 0, 9, 0))
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		before := tm.DueAt()
		t.Run("When paused Friday 20:00 to the next Monday 06:00 (no working time inside)", func(t *testing.T) {
			mustPause(t, tm, ts(loc, 4, 20, 0))
			mustResume(t, tm, ts(loc, 7, 6, 0))
			t.Run("Then DueAt does not move", func(t *testing.T) {
				if due := tm.DueAt(); !due.Equal(before) {
					t.Fatalf("DueAt = %v, want unchanged %v", due, before)
				}
			})
		})
	})
}

func TestGolden8EscalationOnce(t *testing.T) {
	loc := nyLoc(t)
	s := stdSchedule(loc)
	cfg := Config{
		Schedule: s,
		Budget:   8 * time.Hour,
		Levels:   []Level{{Name: "warn", At: 4 * time.Hour}, {Name: "breach", At: 8 * time.Hour}},
	}
	t.Run("Given a timer with warn@4h and breach@8h started Monday 09:00", func(t *testing.T) {
		tm, err := Start(cfg, ts(loc, 0, 9, 0))
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		t.Run("When polled at 5 working hours (Monday 14:00)", func(t *testing.T) {
			f1 := tm.Fired(ts(loc, 0, 14, 0))
			t.Run("Then only warn has fired, at the 4-working-hour instant Monday 13:00", func(t *testing.T) {
				if got := names(f1); len(got) != 1 || got[0] != "warn" {
					t.Fatalf("fired = %v, want [warn]", got)
				}
				if !f1[0].At.Equal(ts(loc, 0, 13, 0)) {
					t.Fatalf("warn fired-at = %v, want Mon 13:00", f1[0].At)
				}
			})
			t.Run("When polled again at 9 working hours (Tuesday 10:00)", func(t *testing.T) {
				f2 := tm.Fired(ts(loc, 1, 10, 0))
				t.Run("Then both warn and breach have fired", func(t *testing.T) {
					if got := names(f2); len(got) != 2 || got[0] != "warn" || got[1] != "breach" {
						t.Fatalf("fired = %v, want [warn breach]", got)
					}
				})
				t.Run("Then warn's fired-at instant is unchanged (write-once)", func(t *testing.T) {
					if !f2[0].At.Equal(f1[0].At) {
						t.Fatalf("warn fired-at moved: %v then %v", f1[0].At, f2[0].At)
					}
				})
				t.Run("Then polling once more never re-emits or rewrites warn", func(t *testing.T) {
					f3 := tm.Fired(ts(loc, 1, 10, 0))
					if got := names(f3); len(got) != 2 {
						t.Fatalf("fired = %v, want stable [warn breach]", got)
					}
					if !f3[0].At.Equal(f1[0].At) {
						t.Fatalf("warn fired-at rewritten on repoll: %v", f3[0].At)
					}
				})
			})
		})
	})
}

func TestGolden9SnapshotRoundTrip(t *testing.T) {
	loc := nyLoc(t)
	s := stdSchedule(loc)
	cfg := Config{
		Schedule: s,
		Budget:   8 * time.Hour,
		Levels:   []Level{{Name: "warn", At: 4 * time.Hour}, {Name: "breach", At: 8 * time.Hour}},
	}
	t.Run("Given a timer started, paused, resumed, with warn fired", func(t *testing.T) {
		tm, err := Start(cfg, ts(loc, 0, 9, 0))
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		mustPause(t, tm, ts(loc, 0, 11, 0))
		mustResume(t, tm, ts(loc, 0, 14, 0))
		// With the 11:00–14:00 pause excluded, 4 working hours elapse at 16:00
		// (09–11 = 2h, then 14–16 = 2h), so warn fires by 16:30.
		pollWarn := ts(loc, 0, 16, 30)
		warn := tm.Fired(pollWarn)
		if len(warn) != 1 || warn[0].Level.Name != "warn" {
			t.Fatalf("setup: fired = %v, want [warn]", names(warn))
		}
		dueBefore := tm.DueAt()

		t.Run("When snapshotted, JSON round-tripped and restored", func(t *testing.T) {
			rest := restoreVia(t, cfg, tm.Snapshot())
			t.Run("Then DueAt is identical", func(t *testing.T) {
				if !rest.DueAt().Equal(dueBefore) {
					t.Fatalf("restored DueAt = %v, want %v", rest.DueAt(), dueBefore)
				}
			})
			t.Run("Then the warn firing is not re-emitted and keeps its instant", func(t *testing.T) {
				f := rest.Fired(pollWarn)
				if len(f) != 1 || f[0].Level.Name != "warn" {
					t.Fatalf("restored fired = %v, want [warn]", names(f))
				}
				if !f[0].At.Equal(warn[0].At) {
					t.Fatalf("restored warn fired-at = %v, want %v", f[0].At, warn[0].At)
				}
				if !warn[0].At.Equal(ts(loc, 0, 16, 0)) {
					t.Fatalf("warn fired-at = %v, want Mon 16:00", warn[0].At)
				}
			})
			t.Run("Then Breached and Remaining match the original thereafter", func(t *testing.T) {
				now := ts(loc, 1, 16, 0)
				if rest.Breached(now) != tm.Breached(now) {
					t.Fatalf("Breached mismatch after restore")
				}
				if rest.Remaining(now) != tm.Remaining(now) {
					t.Fatalf("Remaining mismatch after restore")
				}
			})
		})
	})
}

// names extracts the level names from a firing slice, in order.
func names(fs []Firing) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Level.Name
	}
	return out
}

func mustPause(t *testing.T, tm *Timer, at time.Time) {
	t.Helper()
	if err := tm.Pause(at); err != nil {
		t.Fatalf("Pause(%v): %v", at, err)
	}
}

func mustResume(t *testing.T, tm *Timer, at time.Time) {
	t.Helper()
	if err := tm.Resume(at); err != nil {
		t.Fatalf("Resume(%v): %v", at, err)
	}
}
