package sundial

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// restoreVia marshals a snapshot through encoding/json and restores a timer from
// the decoded copy, mirroring a real persist-and-restart.
func restoreVia(t *testing.T, cfg Config, snap Snapshot) *Timer {
	t.Helper()
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	var s Snapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	rest, err := Restore(cfg, s)
	if err != nil {
		t.Fatalf("restore: %v", err)
	}
	return rest
}

func TestSnapshotOpenPauseSurvives(t *testing.T) {
	loc := nyLoc(t)
	cfg := Config{Schedule: stdSchedule(loc), Budget: 8 * time.Hour}
	t.Run("Given a timer frozen by an open pause at 2 working hours", func(t *testing.T) {
		tm, err := Start(cfg, ts(loc, 0, 9, 0))
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		mustPause(t, tm, ts(loc, 0, 11, 0))
		frozen := tm.Elapsed(ts(loc, 2, 16, 0))

		t.Run("When snapshotted, round-tripped and restored", func(t *testing.T) {
			rest := restoreVia(t, cfg, tm.Snapshot())
			t.Run("Then the restored timer is still paused and still frozen", func(t *testing.T) {
				if !rest.Paused() {
					t.Fatal("restored timer is not paused")
				}
				if e := rest.Elapsed(ts(loc, 2, 16, 0)); e != frozen {
					t.Fatalf("restored Elapsed = %v, want frozen %v", e, frozen)
				}
			})
			t.Run("Then resuming the restored timer behaves as a continuation", func(t *testing.T) {
				if err := rest.Resume(ts(loc, 2, 11, 0)); err != nil {
					t.Fatalf("Resume: %v", err)
				}
				if e := rest.Elapsed(ts(loc, 2, 13, 0)); e != 4*time.Hour {
					t.Fatalf("Elapsed after resume = %v, want 4h", e)
				}
			})
		})
	})
}

func TestSnapshotBreachMarkerSurvives(t *testing.T) {
	loc := nyLoc(t)
	cfg := Config{
		Schedule: stdSchedule(loc),
		Budget:   8 * time.Hour,
		Levels:   []Level{{"warn", 4 * time.Hour}, {"breach", 8 * time.Hour}},
	}
	t.Run("Given a breached timer with both levels fired", func(t *testing.T) {
		tm, err := Start(cfg, ts(loc, 0, 9, 0))
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		now := ts(loc, 1, 12, 0)
		tm.Breached(now)
		fired := tm.Fired(now)
		wantAt, _ := tm.BreachedAt()

		t.Run("When round-tripped and restored", func(t *testing.T) {
			rest := restoreVia(t, cfg, tm.Snapshot())
			t.Run("Then the breach marker and instant survive", func(t *testing.T) {
				at, ok := rest.BreachedAt()
				if !ok || !at.Equal(wantAt) {
					t.Fatalf("restored BreachedAt = (%v,%v), want (%v,true)", at, ok, wantAt)
				}
			})
			t.Run("Then the firing instants survive unchanged", func(t *testing.T) {
				got := rest.Fired(now)
				if len(got) != len(fired) {
					t.Fatalf("restored fired count %d, want %d", len(got), len(fired))
				}
				for i := range got {
					if got[i].Level != fired[i].Level || !got[i].At.Equal(fired[i].At) {
						t.Fatalf("firing %d differs: %+v vs %+v", i, got[i], fired[i])
					}
				}
			})
		})
	})
}

func TestSnapshotEmpty(t *testing.T) {
	loc := nyLoc(t)
	cfg := Config{Schedule: stdSchedule(loc), Budget: 8 * time.Hour}
	t.Run("Given a fresh, untouched timer", func(t *testing.T) {
		tm, err := Start(cfg, ts(loc, 0, 9, 0))
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		t.Run("When snapshotted", func(t *testing.T) {
			s := tm.Snapshot()
			t.Run("Then the snapshot carries no pauses or markers and restores usable", func(t *testing.T) {
				if len(s.Pauses) != 0 || len(s.Fired) != 0 || s.Breached {
					t.Fatalf("empty snapshot carried state: %+v", s)
				}
				rest := restoreVia(t, cfg, s)
				if !rest.DueAt().Equal(tm.DueAt()) {
					t.Fatalf("restored DueAt = %v, want %v", rest.DueAt(), tm.DueAt())
				}
			})
		})
	})
}

func TestSnapshotIsACopy(t *testing.T) {
	loc := nyLoc(t)
	cfg := Config{Schedule: stdSchedule(loc), Budget: 8 * time.Hour}
	t.Run("Given a snapshot taken mid-pause", func(t *testing.T) {
		tm, err := Start(cfg, ts(loc, 0, 9, 0))
		if err != nil {
			t.Fatalf("Start: %v", err)
		}
		mustPause(t, tm, ts(loc, 0, 11, 0))
		s := tm.Snapshot()
		t.Run("When the timer keeps mutating after the snapshot", func(t *testing.T) {
			mustResume(t, tm, ts(loc, 0, 14, 0))
			mustPause(t, tm, ts(loc, 0, 15, 0))
			t.Run("Then the snapshot is an unchanged point-in-time copy", func(t *testing.T) {
				if len(s.Pauses) != 1 || !s.Pauses[0].Open {
					t.Fatalf("snapshot pauses mutated: %+v", s.Pauses)
				}
			})
		})
	})
}

func TestRestoreRejectsBadSnapshot(t *testing.T) {
	loc := nyLoc(t)
	cfg := Config{
		Schedule: stdSchedule(loc),
		Budget:   8 * time.Hour,
		Levels:   []Level{{"warn", 4 * time.Hour}},
	}
	start := ts(loc, 0, 9, 0)
	t.Run("Given malformed snapshots", func(t *testing.T) {
		t.Run("When a fired marker references a level index out of range", func(t *testing.T) {
			s := Snapshot{Start: start, Fired: []LevelFiring{{Index: 5, At: start}}}
			t.Run("Then Restore returns ErrBadSnapshot", func(t *testing.T) {
				if _, err := Restore(cfg, s); !errors.Is(err, ErrBadSnapshot) {
					t.Fatalf("Restore = %v, want ErrBadSnapshot", err)
				}
			})
		})
		t.Run("When a pause starts before the previous resume", func(t *testing.T) {
			s := Snapshot{
				Start: start,
				Pauses: []PauseSpan{
					{Start: ts(loc, 0, 10, 0), End: ts(loc, 0, 14, 0)},
					{Start: ts(loc, 0, 12, 0), End: ts(loc, 0, 15, 0)}, // overlaps the first
				},
			}
			t.Run("Then Restore returns ErrBadSnapshot", func(t *testing.T) {
				if _, err := Restore(cfg, s); !errors.Is(err, ErrBadSnapshot) {
					t.Fatalf("Restore = %v, want ErrBadSnapshot", err)
				}
			})
		})
		t.Run("When the config itself is invalid", func(t *testing.T) {
			t.Run("Then Restore surfaces the config error", func(t *testing.T) {
				if _, err := Restore(Config{Schedule: Schedule{}, Budget: time.Hour}, Snapshot{Start: start}); !errors.Is(err, ErrBadSchedule) {
					t.Fatalf("Restore = %v, want ErrBadSchedule", err)
				}
			})
		})
	})
}

func TestRestoreDefendsAgainstOverSubtractingPause(t *testing.T) {
	loc := nyLoc(t)
	cfg := Config{Schedule: stdSchedule(loc), Budget: 8 * time.Hour}
	t.Run("Given a hand-built snapshot whose pause reaches into working time before start", func(t *testing.T) {
		// start mid-window at 10:00; a pause 09:00–11:00 would subtract more
		// working time than [start, now) contains — elapsed must clamp at zero.
		s := Snapshot{
			Start:  ts(loc, 0, 10, 0),
			Pauses: []PauseSpan{{Start: ts(loc, 0, 9, 0), End: ts(loc, 0, 11, 0)}},
		}
		t.Run("When restored and inspected inside the pause window", func(t *testing.T) {
			rest, err := Restore(cfg, s)
			if err != nil {
				t.Fatalf("Restore: %v", err)
			}
			t.Run("Then elapsed never goes negative", func(t *testing.T) {
				if e := rest.Elapsed(ts(loc, 0, 11, 0)); e != 0 {
					t.Fatalf("Elapsed = %v, want 0 (clamped)", e)
				}
			})
		})
	})
}

func TestRestoreClampsClockToStart(t *testing.T) {
	loc := nyLoc(t)
	cfg := Config{Schedule: stdSchedule(loc), Budget: 8 * time.Hour}
	start := ts(loc, 0, 9, 0)
	t.Run("Given a snapshot whose clock predates its start", func(t *testing.T) {
		s := Snapshot{Start: start, Clock: ts(loc, 0, 6, 0)}
		t.Run("When restored and then paused with an earlier instant", func(t *testing.T) {
			rest, err := Restore(cfg, s)
			if err != nil {
				t.Fatalf("Restore: %v", err)
			}
			mustPause(t, rest, ts(loc, 0, 7, 0)) // before start; must clamp forward to start
			t.Run("Then the pause is clamped forward to at least the start", func(t *testing.T) {
				snap := rest.Snapshot()
				if len(snap.Pauses) != 1 || snap.Pauses[0].Start.Before(start) {
					t.Fatalf("pause not clamped to start: %+v", snap.Pauses)
				}
			})
		})
	})
}
