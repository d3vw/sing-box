package quota

import (
	"math/rand/v2"
	"sort"
	"time"
)

// The portal's forecast estimates whether the remaining quota lasts until the
// next reset by resampling past days: each run stitches together blocks of
// consecutive historical days until the reset, and the share of runs that
// exhaust the quota is the chance of running out.
//   - Blocks of forecastBlock days keep "several heavy days in a row" habits.
//   - The last forecastRecentDays are twice as likely to be drawn, so the
//     forecast follows a change in habits sooner.
//   - With four weeks of history, a day is only drawn from the same weekday.
//   - The seed is the day's number, so refreshes within a day agree.
const (
	forecastRuns          = 2000
	forecastBlock         = 3
	forecastMinDays       = 7
	forecastWeekdayDays   = 28
	forecastRecentDays    = 7
	forecastRecentWeight  = 2
	forecastOKMaxRunOut   = 0.05
	forecastWarnMaxRunOut = 0.5
)

type forecastInput struct {
	// History is the usage of each past full day, oldest first, ending
	// yesterday; Weekdays holds the weekday of each.
	History  []int64
	Weekdays []time.Weekday
	Remain   int64
	// RestOfToday is the fraction of today still to come, and LastDay the
	// number of whole days after today before the reset.
	RestOfToday float64
	LastDay     int
	Today       time.Weekday
	Seed        uint64
}

type forecastResult struct {
	// RunOut is the share of runs that exhaust the quota before the reset.
	RunOut float64
	// LeftP50 is the median quota left at the reset, 0 when exhausted.
	LeftP50 int64
	// OutP25 and OutP75 bound the day (0 is today) runs exhaust the quota,
	// counting only runs that do; HasOut is false when none do.
	OutP25, OutP75 int
	HasOut         bool
}

func (f forecastResult) tone() string {
	switch {
	case f.RunOut < forecastOKMaxRunOut:
		return "ok"
	case f.RunOut < forecastWarnMaxRunOut:
		return "warn"
	default:
		return "err"
	}
}

func forecastRunOut(in forecastInput) (forecastResult, bool) {
	hist := in.History
	if len(hist) < forecastMinDays {
		return forecastResult{}, false
	}
	if in.Remain <= 0 {
		return forecastResult{RunOut: 1, HasOut: true}, true
	}

	type group struct {
		idx []int
		cum []int
	}
	byWeekday := len(hist) >= forecastWeekdayDays
	groups := make([]group, 1)
	if byWeekday {
		groups = make([]group, 7)
	}
	groupOf := func(weekday time.Weekday) *group {
		if byWeekday {
			return &groups[weekday]
		}
		return &groups[0]
	}
	for i := range hist {
		g := groupOf(in.Weekdays[i])
		weight := 1
		if i >= len(hist)-forecastRecentDays {
			weight = forecastRecentWeight
		}
		total := weight
		if len(g.cum) > 0 {
			total += g.cum[len(g.cum)-1]
		}
		g.idx = append(g.idx, i)
		g.cum = append(g.cum, total)
	}

	random := rand.New(rand.NewPCG(in.Seed, in.Seed))
	pickStart := func(day int) int {
		g := groupOf((in.Today + time.Weekday(day)) % 7)
		r := random.IntN(g.cum[len(g.cum)-1])
		return g.idx[sort.SearchInts(g.cum, r+1)]
	}

	lefts := make([]int64, 0, forecastRuns)
	outDays := make([]int, 0, forecastRuns)
	for run := 0; run < forecastRuns; run++ {
		var used float64
		outDay := -1
		for day := 0; day <= in.LastDay; {
			start := pickStart(day)
			// Take up to a block of days, drawing a new start at the end of
			// the history.
			for j := 0; j < forecastBlock && day <= in.LastDay && start+j < len(hist); j, day = j+1, day+1 {
				share := 1.0
				if day == 0 {
					share = in.RestOfToday
				}
				used += float64(hist[start+j]) * share
				if outDay < 0 && used >= float64(in.Remain) {
					outDay = day
				}
			}
		}
		lefts = append(lefts, max(0, in.Remain-int64(used)))
		if outDay >= 0 {
			outDays = append(outDays, outDay)
		}
	}

	sort.Slice(lefts, func(i, j int) bool { return lefts[i] < lefts[j] })
	sort.Ints(outDays)
	result := forecastResult{
		RunOut:  float64(len(outDays)) / forecastRuns,
		LeftP50: quantile(lefts, 0.5),
	}
	if len(outDays) > 0 {
		result.OutP25, result.OutP75, result.HasOut = quantile(outDays, 0.25), quantile(outDays, 0.75), true
	}
	return result, true
}

// quantile returns the nearest item to the q-th quantile of a sorted slice.
func quantile[T any](sorted []T, q float64) T {
	i := int(q*float64(len(sorted)-1) + 0.5)
	return sorted[min(len(sorted)-1, max(0, i))]
}
