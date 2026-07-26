// Command examples wraps the pure sundial core with a real wall-clock ticker and
// shows the two things the library deliberately leaves to the caller: driving
// the clock (sundial owns no goroutine or timer) and supplying the holiday set.
//
// It runs a compressed simulation — one simulated working hour per few
// milliseconds — of an 8 working-hour SLA ticket over a Mon–Fri 09:00–17:00
// schedule, with a customer-wait pause in the middle and warn/breach escalation
// levels, printing each state change a real dashboard would act on.
package main

import (
	"fmt"
	"time"

	"github.com/zkrebbekx/sundial"
)

func main() {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		loc = time.UTC // fall back so the example still runs without a zone db
	}

	// The holiday set is the caller's to supply. A caller wanting country
	// calendars would map, say, rickar/cal's dates into this set; here we map a
	// couple of fixed dates in by hand to show the shape. sundial ships none.
	holidays := sundial.Holidays(
		sundial.On(2026, time.January, 1),  // New Year's Day
		sundial.On(2026, time.January, 19), // a holiday Monday
	)

	sched := sundial.Schedule{
		Loc:      loc,
		Week:     sundial.Weekdays(sundial.Window{Open: sundial.At(9, 0, 0), Close: sundial.At(17, 0, 0)}),
		Holidays: holidays,
	}

	cfg := sundial.Config{
		Schedule: sched,
		Budget:   8 * time.Hour,
		Levels: []sundial.Level{
			{Name: "warn", At: 4 * time.Hour},
			{Name: "breach", At: 8 * time.Hour},
		},
	}

	// A ticket opened Friday 15:00: the budget spills across the weekend.
	start := time.Date(2026, time.January, 16, 15, 0, 0, 0, loc)
	timer, err := sundial.Start(cfg, start)
	if err != nil {
		panic(err)
	}
	fmt.Printf("ticket opened %s\n", start.Format("Mon 2006-01-02 15:04"))
	fmt.Printf("due (8 working hours) %s\n\n", timer.DueAt().Format("Mon 2006-01-02 15:04"))

	// The pure core has no wall clock; supply one. We advance a *simulated* now
	// in fixed working-time-ish steps and poll the timer at each tick, exactly
	// as a background job polling on a cron would. A customer-wait pause is
	// applied partway through and released later.
	// A single customer-wait window: pause Tuesday 10:00, resume Tuesday 11:00
	// (both inside working hours, so it shifts the due instant later by 1h).
	waitStart := time.Date(2026, time.January, 20, 10, 0, 0, 0, loc)
	waitEnd := time.Date(2026, time.January, 20, 11, 0, 0, 0, loc)

	sim := start
	paused, waited := false, false
	step := 30 * time.Minute
	ticker := time.NewTicker(2 * time.Millisecond)
	defer ticker.Stop()

	seen := map[string]bool{}
	lastRemaining := timer.Remaining(sim)
	for range ticker.C {
		sim = sim.Add(step)

		if !waited && !paused && !sim.Before(waitStart) {
			_ = timer.Pause(waitStart)
			paused = true
			fmt.Printf("%s  paused (waiting on customer)\n", sim.Format("Mon 15:04"))
		}
		if paused && !sim.Before(waitEnd) {
			_ = timer.Resume(waitEnd)
			paused, waited = false, true
			fmt.Printf("%s  resumed; due shifted to %s\n", sim.Format("Mon 15:04"), timer.DueAt().Format("Mon 2006-01-02 15:04"))
		}

		for _, f := range timer.Fired(sim) {
			if !seen[f.Level.Name] {
				seen[f.Level.Name] = true
				fmt.Printf("%s  ESCALATION %-6s fired (at %s)\n",
					sim.Format("Mon 15:04"), f.Level.Name, f.At.Format("Mon 2006-01-02 15:04"))
			}
		}

		if timer.Breached(sim) {
			at, _ := timer.BreachedAt()
			fmt.Printf("%s  BREACHED (budget exhausted at %s)\n", sim.Format("Mon 15:04"), at.Format("Mon 2006-01-02 15:04"))
			break
		}

		// Print the routine line only when the clock actually advanced — that
		// is, during working time — so the idle weekend collapses to nothing.
		if rem := timer.Remaining(sim); rem != lastRemaining {
			fmt.Printf("%s  remaining %s\n", sim.Format("Mon 15:04"), rem.Round(time.Minute))
			lastRemaining = rem
		}
	}
}
