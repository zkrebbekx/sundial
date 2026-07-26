//go:build js && wasm

package main

import (
	"encoding/json"
	"fmt"
)

// A scenario is a named preset: a complete, accurate timer configuration that
// teaches one idea in about fifteen seconds. sundial is a pure clock with no
// stored state, so a scenario is simply the inputs the page loads into its
// controls — the schedule, budget, levels, start, now and pauses — plus which
// panel to focus and a one-line note on what to watch. The numbers are pinned
// to the library's golden vectors (docs/DESIGN.md), so loading one and reading
// the timeline reproduces a figure from the README exactly.
//
// The schedule is given compactly as a single Mon–Fri window (weekOpen /
// weekClose); the page expands it to the seven-day week. Instants are civil
// date-times in the scenario's zone. Durations are whole hours.
type scenario struct {
	Name   string         `json:"name"`
	Title  string         `json:"title"`
	Blurb  string         `json:"blurb"`
	Panel  string         `json:"panel"`
	Note   string         `json:"note"`
	Config map[string]any `json:"config"`
}

// scenarios are the loadable presets, keyed by name.
var scenarios = map[string]*scenario{
	"support-8h": {
		Name:  "support-8h",
		Title: "8-hour support SLA",
		Blurb: "A ticket opened late on Friday afternoon. The 8-working-hour clock runs out Monday 16:00 — the weekend never counts.",
		Panel: "timeline",
		Note: "Mon–Fri 09:00–17:00, budget 8 working hours, opened Friday 16:00. Drag the now handle across " +
			"the night and the weekend: elapsed only climbs inside the light windows. Due lands Monday 16:00 — " +
			"1 hour Friday plus 7 hours Monday.",
		Config: map[string]any{
			"tz":          "America/New_York",
			"weekOpen":    "09:00",
			"weekClose":   "17:00",
			"holidays":    []string{},
			"budgetHours": 8,
			"levels": []map[string]any{
				{"name": "warn", "hours": 4},
				{"name": "breach", "hours": 8},
			},
			"start":  "2026-01-09T16:00:00",
			"now":    "2026-01-12T11:00:00",
			"pauses": []map[string]any{},
		},
	},

	"overnight-weekend": {
		Name:  "overnight-weekend",
		Title: "Overnight + weekend skip",
		Blurb: "The dark regions carry no time. From Friday 16:00, two working hours take you to Monday 10:00.",
		Panel: "timeline",
		Note: "Standard 09:00–17:00 week. From Friday 16:00, one working hour reaches Friday 17:00, then the clock " +
			"jumps the whole night and weekend to Monday 09:00 for the second hour — Monday 10:00 is two working " +
			"hours in. Drag now and watch elapsed freeze across every dark stretch.",
		Config: map[string]any{
			"tz":          "America/New_York",
			"weekOpen":    "09:00",
			"weekClose":   "17:00",
			"holidays":    []string{},
			"budgetHours": 8,
			"levels": []map[string]any{
				{"name": "warn", "hours": 4},
				{"name": "breach", "hours": 8},
			},
			"start":  "2026-01-09T16:00:00",
			"now":    "2026-01-12T10:00:00",
			"pauses": []map[string]any{},
		},
	},

	"spring-forward": {
		Name:  "spring-forward",
		Title: "Spring-forward (DST)",
		Blurb: "A budget crossing US spring-forward. Due lands on the right civil instant while the real span is one hour shorter.",
		Panel: "dst",
		Note: "Friday 2025-03-07 16:00 in America/New_York, 8 working hours. Due is Monday 16:00 — the civil instant " +
			"the arithmetic expects — yet the real wall-clock span is 71 hours, one hour shorter, because the clocks " +
			"jumped forward at 02:00 Sunday. Windows are civil-time, so 09:00–17:00 is eight hours either way.",
		Config: map[string]any{
			"tz":          "America/New_York",
			"weekOpen":    "09:00",
			"weekClose":   "17:00",
			"holidays":    []string{},
			"budgetHours": 8,
			"levels": []map[string]any{
				{"name": "warn", "hours": 4},
				{"name": "breach", "hours": 8},
			},
			"start":  "2025-03-07T16:00:00",
			"now":    "2025-03-10T16:00:00",
			"pauses": []map[string]any{},
		},
	},

	"paused-customer": {
		Name:  "paused-customer",
		Title: "Paused on customer",
		Blurb: "Waiting on the customer freezes the clock. A three-hour pause pushes the Monday-17:00 due to Tuesday 12:00.",
		Panel: "pause",
		Note: "8-hour timer opened Monday 09:00 (due Monday 17:00). A pause 11:00–14:00 holds three working hours, so " +
			"elapsed freezes across it and due shifts to Tuesday 12:00. Move the pause onto the weekend and it shifts " +
			"nothing — the weekend holds no working time. Leave it open and elapsed freezes at the pause instant.",
		Config: map[string]any{
			"tz":          "America/New_York",
			"weekOpen":    "09:00",
			"weekClose":   "17:00",
			"holidays":    []string{},
			"budgetHours": 8,
			"levels": []map[string]any{
				{"name": "warn", "hours": 4},
				{"name": "breach", "hours": 8},
			},
			"start": "2026-01-05T09:00:00",
			"now":   "2026-01-06T12:00:00",
			"pauses": []map[string]any{
				{"start": "2026-01-05T11:00:00", "end": "2026-01-05T14:00:00"},
			},
		},
	},
}

// scenarioOrder fixes the presentation order: the plain SLA first, then the two
// skip stories, DST, and the pause.
var scenarioOrder = []string{"support-8h", "overnight-weekend", "spring-forward", "paused-customer"}

func scenarioList() []map[string]any {
	out := make([]map[string]any, 0, len(scenarioOrder))
	for _, name := range scenarioOrder {
		sc, ok := scenarios[name]
		if !ok {
			continue
		}
		out = append(out, map[string]any{
			"name":  sc.Name,
			"title": sc.Title,
			"blurb": sc.Blurb,
			"panel": sc.Panel,
		})
	}
	return out
}

// handleLoadScenario returns a scenario's full preset by name. The name may
// arrive as a bare string or as {"name": ...}.
func handleLoadScenario(in []byte) (any, error) {
	name := scenarioName(in)
	sc, ok := scenarios[name]
	if !ok {
		return nil, badArg("unknown_scenario", fmt.Sprintf("unknown scenario %q", name))
	}
	return sc, nil
}

// scenarioName extracts the requested name from a bare string or a {name}
// object, so loadScenario("support-8h") and loadScenario({name:"..."}) both work.
func scenarioName(in []byte) string {
	s := string(in)
	if s == "" || s == "{}" {
		return ""
	}
	if s[0] != '{' {
		return s
	}
	var obj struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(in, &obj); err == nil {
		return obj.Name
	}
	return ""
}
