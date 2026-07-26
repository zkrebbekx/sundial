package sundial

import (
	"errors"
	"testing"
	"time"
)

func TestConfigValidation(t *testing.T) {
	loc := nyLoc(t)
	s := stdSchedule(loc)
	t.Run("Given assorted configurations", func(t *testing.T) {
		cases := []struct {
			name string
			cfg  Config
			want error // nil means valid
		}{
			{"well-formed", Config{Schedule: s, Budget: 8 * time.Hour}, nil},
			{"zero budget", Config{Schedule: s, Budget: 0}, nil},
			{"with ascending levels", Config{Schedule: s, Budget: 8 * time.Hour, Levels: []Level{{"warn", 4 * time.Hour}, {"breach", 8 * time.Hour}}}, nil},
			{"invalid schedule propagates", Config{Schedule: Schedule{}, Budget: time.Hour}, ErrBadSchedule},
			{"negative budget", Config{Schedule: s, Budget: -time.Hour}, ErrBadBudget},
			{"negative level threshold", Config{Schedule: s, Budget: time.Hour, Levels: []Level{{"bad", -time.Hour}}}, ErrBadLevels},
			{"non-ascending levels", Config{Schedule: s, Budget: time.Hour, Levels: []Level{{"a", 4 * time.Hour}, {"b", 4 * time.Hour}}}, ErrBadLevels},
			{"descending levels", Config{Schedule: s, Budget: time.Hour, Levels: []Level{{"a", 8 * time.Hour}, {"b", 4 * time.Hour}}}, ErrBadLevels},
			{"empty schedule with positive budget", Config{Schedule: Schedule{Loc: loc}, Budget: time.Hour}, ErrBadSchedule},
			{"empty schedule with positive level", Config{Schedule: Schedule{Loc: loc}, Budget: 0, Levels: []Level{{"warn", time.Hour}}}, ErrBadSchedule},
			{"empty schedule, zero budget, zero level is fine", Config{Schedule: Schedule{Loc: loc}, Budget: 0, Levels: []Level{{"immediate", 0}}}, nil},
		}
		for _, tc := range cases {
			t.Run("When starting with "+tc.name, func(t *testing.T) {
				t.Run("Then Start reports the expected result", func(t *testing.T) {
					tm, err := Start(tc.cfg, ts(loc, 0, 9, 0))
					if tc.want == nil {
						if err != nil {
							t.Fatalf("Start = %v, want nil", err)
						}
						if tm == nil {
							t.Fatal("Start returned nil timer for a valid config")
						}
						return
					}
					if !errors.Is(err, tc.want) {
						t.Fatalf("Start = %v, want %v", err, tc.want)
					}
					if tm != nil {
						t.Fatalf("timer = %v, want nil on error", tm)
					}
				})
			})
		}
	})
}

func TestRemainingAndBreach(t *testing.T) {
	loc := nyLoc(t)
	s := stdSchedule(loc)
	cfg := Config{Schedule: s, Budget: 8 * time.Hour}
	t.Run("Given an 8h timer started Monday 09:00", func(t *testing.T) {
		tm, err := Start(cfg, ts(loc, 0, 9, 0))
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		t.Run("When two working hours have elapsed (Monday 11:00)", func(t *testing.T) {
			now := ts(loc, 0, 11, 0)
			t.Run("Then Elapsed is 2h and Remaining is 6h and not breached", func(t *testing.T) {
				if e := tm.Elapsed(now); e != 2*time.Hour {
					t.Fatalf("Elapsed = %v, want 2h", e)
				}
				if r := tm.Remaining(now); r != 6*time.Hour {
					t.Fatalf("Remaining = %v, want 6h", r)
				}
				if tm.Breached(now) {
					t.Fatal("Breached = true, want false")
				}
			})
		})
		t.Run("When the budget is exactly met (Monday 17:00)", func(t *testing.T) {
			now := ts(loc, 0, 17, 0)
			t.Run("Then Remaining is 0 and it is breached at the due instant", func(t *testing.T) {
				if r := tm.Remaining(now); r != 0 {
					t.Fatalf("Remaining = %v, want 0", r)
				}
				if !tm.Breached(now) {
					t.Fatal("Breached = false, want true")
				}
				at, ok := tm.BreachedAt()
				if !ok || !at.Equal(ts(loc, 0, 17, 0)) {
					t.Fatalf("BreachedAt = (%v,%v), want (Mon 17:00,true)", at, ok)
				}
			})
		})
		t.Run("When queried before the start instant", func(t *testing.T) {
			now := ts(loc, 0, 8, 0)
			t.Run("Then elapsed is zero and remaining is the full budget", func(t *testing.T) {
				if e := tm.Elapsed(now); e != 0 {
					t.Fatalf("Elapsed = %v, want 0", e)
				}
				if r := tm.Remaining(now); r != 8*time.Hour {
					t.Fatalf("Remaining = %v, want 8h", r)
				}
			})
		})
	})
}

func TestBreachInstantWriteOnce(t *testing.T) {
	loc := nyLoc(t)
	cfg := Config{Schedule: stdSchedule(loc), Budget: 8 * time.Hour}
	t.Run("Given a breached timer first observed late", func(t *testing.T) {
		tm, err := Start(cfg, ts(loc, 0, 9, 0))
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		// First observe breach far past the due instant.
		if !tm.Breached(ts(loc, 3, 12, 0)) {
			t.Fatal("expected breach")
		}
		at1, _ := tm.BreachedAt()
		t.Run("When observed again at a different now", func(t *testing.T) {
			tm.Breached(ts(loc, 4, 9, 0))
			at2, _ := tm.BreachedAt()
			t.Run("Then the breach instant is the true due instant and never moves", func(t *testing.T) {
				if !at1.Equal(ts(loc, 0, 17, 0)) {
					t.Fatalf("BreachedAt = %v, want Mon 17:00 (the crossing instant)", at1)
				}
				if !at2.Equal(at1) {
					t.Fatalf("BreachedAt moved: %v then %v", at1, at2)
				}
			})
		})
	})
}

func TestZeroBudget(t *testing.T) {
	loc := nyLoc(t)
	t.Run("Given a zero-budget timer", func(t *testing.T) {
		cfg := Config{Schedule: stdSchedule(loc), Budget: 0}
		start := ts(loc, 0, 9, 0)
		tm, err := Start(cfg, start)
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		t.Run("When inspected", func(t *testing.T) {
			t.Run("Then DueAt is the start and it is breached from the start", func(t *testing.T) {
				if !tm.DueAt().Equal(start) {
					t.Fatalf("DueAt = %v, want %v", tm.DueAt(), start)
				}
				if !tm.Breached(start) {
					t.Fatal("Breached = false, want true for a zero budget")
				}
			})
		})
	})
}

func TestPauseResumeErrors(t *testing.T) {
	loc := nyLoc(t)
	cfg := Config{Schedule: stdSchedule(loc), Budget: 8 * time.Hour}
	t.Run("Given a fresh timer", func(t *testing.T) {
		tm, err := Start(cfg, ts(loc, 0, 9, 0))
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		t.Run("When resuming without an open pause", func(t *testing.T) {
			t.Run("Then it returns ErrNotPaused and does not panic", func(t *testing.T) {
				if err := tm.Resume(ts(loc, 0, 10, 0)); !errors.Is(err, ErrNotPaused) {
					t.Fatalf("Resume = %v, want ErrNotPaused", err)
				}
			})
		})
		t.Run("When pausing twice without a resume between", func(t *testing.T) {
			mustPause(t, tm, ts(loc, 0, 11, 0))
			t.Run("Then the second pause returns ErrAlreadyPaused", func(t *testing.T) {
				if err := tm.Pause(ts(loc, 0, 12, 0)); !errors.Is(err, ErrAlreadyPaused) {
					t.Fatalf("Pause = %v, want ErrAlreadyPaused", err)
				}
				if !tm.Paused() {
					t.Fatal("Paused = false, want true")
				}
			})
		})
	})
}

func TestOpenPauseFreezesClock(t *testing.T) {
	loc := nyLoc(t)
	cfg := Config{Schedule: stdSchedule(loc), Budget: 8 * time.Hour}
	t.Run("Given an 8h timer paused at Monday 11:00 with no resume", func(t *testing.T) {
		tm, err := Start(cfg, ts(loc, 0, 9, 0))
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		mustPause(t, tm, ts(loc, 0, 11, 0)) // 2 working hours in
		t.Run("When time advances well past the pause", func(t *testing.T) {
			t.Run("Then elapsed stays frozen at the pause instant's 2 working hours", func(t *testing.T) {
				if e := tm.Elapsed(ts(loc, 2, 16, 0)); e != 2*time.Hour {
					t.Fatalf("Elapsed while paused = %v, want frozen 2h", e)
				}
				if r := tm.Remaining(ts(loc, 2, 16, 0)); r != 6*time.Hour {
					t.Fatalf("Remaining while paused = %v, want 6h", r)
				}
				if tm.Breached(ts(loc, 2, 16, 0)) {
					t.Fatal("Breached while paused = true, want false (frozen below budget)")
				}
			})
		})
		t.Run("When it later resumes", func(t *testing.T) {
			mustResume(t, tm, ts(loc, 2, 11, 0)) // resume after two skipped days
			t.Run("Then elapsed advances again from the frozen value", func(t *testing.T) {
				// resume Wed 11:00; +2 working hours by Wed 13:00 → elapsed 4h.
				if e := tm.Elapsed(ts(loc, 2, 13, 0)); e != 4*time.Hour {
					t.Fatalf("Elapsed after resume = %v, want 4h", e)
				}
			})
		})
	})
}

func TestPauseClampForward(t *testing.T) {
	loc := nyLoc(t)
	cfg := Config{Schedule: stdSchedule(loc), Budget: 8 * time.Hour}
	t.Run("Given a timer with a completed pause 11:00–14:00", func(t *testing.T) {
		tm, err := Start(cfg, ts(loc, 0, 9, 0))
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		mustPause(t, tm, ts(loc, 0, 11, 0))
		mustResume(t, tm, ts(loc, 0, 14, 0))
		due := tm.DueAt()
		t.Run("When a later pause is opened with an out-of-order (earlier) instant", func(t *testing.T) {
			// 10:00 is before the last resume at 14:00; it must clamp forward.
			mustPause(t, tm, ts(loc, 0, 10, 0))
			mustResume(t, tm, ts(loc, 0, 10, 0))
			t.Run("Then the clamped zero-length pause adds no working time and DueAt is unchanged", func(t *testing.T) {
				if got := tm.DueAt(); !got.Equal(due) {
					t.Fatalf("DueAt = %v, want unchanged %v", got, due)
				}
			})
		})
	})
}

func TestFiredNoLevels(t *testing.T) {
	loc := nyLoc(t)
	cfg := Config{Schedule: stdSchedule(loc), Budget: 8 * time.Hour}
	t.Run("Given a timer with no levels", func(t *testing.T) {
		tm, err := Start(cfg, ts(loc, 0, 9, 0))
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		t.Run("When polled", func(t *testing.T) {
			t.Run("Then Fired is always empty", func(t *testing.T) {
				if f := tm.Fired(ts(loc, 3, 16, 0)); len(f) != 0 {
					t.Fatalf("Fired = %v, want empty", names(f))
				}
			})
		})
	})
}

func TestStartAccessor(t *testing.T) {
	loc := nyLoc(t)
	cfg := Config{Schedule: stdSchedule(loc), Budget: 8 * time.Hour}
	t.Run("Given a timer", func(t *testing.T) {
		start := ts(loc, 0, 9, 30)
		tm, err := Start(cfg, start)
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		t.Run("When asked for its start", func(t *testing.T) {
			t.Run("Then it reports the start instant", func(t *testing.T) {
				if !tm.Start().Equal(start) {
					t.Fatalf("Start() = %v, want %v", tm.Start(), start)
				}
			})
		})
	})
}
