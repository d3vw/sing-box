package quota

import (
	"testing"
	"time"
)

func steadyForecast(days int, daily, remain int64, lastDay int) forecastInput {
	in := forecastInput{Remain: remain, RestOfToday: 0.5, LastDay: lastDay, Today: time.Monday, Seed: 20000}
	for i := range days {
		in.History = append(in.History, daily)
		in.Weekdays = append(in.Weekdays, time.Weekday((int(time.Monday)-days+i+70)%7))
	}
	return in
}

func TestForecastNeedsAWeekOfHistory(t *testing.T) {
	if _, ok := forecastRunOut(steadyForecast(6, gib, 100*gib, 10)); ok {
		t.Fatal("expected no forecast with fewer than 7 days")
	}
}

func TestForecastSteadyUsage(t *testing.T) {
	// 1 GB a day for 10.5 more days leaves 100 - 10.5 GB.
	result, ok := forecastRunOut(steadyForecast(28, gib, 100*gib, 10))
	if !ok || result.tone() != "ok" || result.HasOut {
		t.Fatalf("expected ok forecast, got %+v", result)
	}
	if want := int64(100*gib - gib*21/2); result.LeftP50 != want {
		t.Fatalf("expected %d left at reset, got %d", want, result.LeftP50)
	}

	// 10 GB a day exhausts 35 GB on day 3 (5 + 10 + 10 + 10 GB).
	result, _ = forecastRunOut(steadyForecast(14, 10*gib, 35*gib, 10))
	if result.tone() != "err" || !result.HasOut || result.OutP25 != 3 || result.OutP75 != 3 || result.LeftP50 != 0 {
		t.Fatalf("expected run out on day 3, got %+v", result)
	}
}

func TestForecastIsStableWithinADay(t *testing.T) {
	// About 1 GB a day for 20.5 days against 20 GB.
	in := steadyForecast(21, 0, 20*gib, 20)
	for i := range in.History {
		in.History[i] = int64(i%5) * gib / 2
	}
	first, _ := forecastRunOut(in)
	second, _ := forecastRunOut(in)
	if first != second {
		t.Fatalf("expected the same forecast for the same seed, got %+v and %+v", first, second)
	}
	if first.RunOut <= 0 || first.RunOut >= 1 {
		t.Fatalf("expected an uncertain forecast for mixed usage, got %+v", first)
	}
}

func TestForecastExhaustedQuota(t *testing.T) {
	result, ok := forecastRunOut(steadyForecast(7, gib, 0, 3))
	if !ok || result.RunOut != 1 || result.tone() != "err" {
		t.Fatalf("expected certain run out, got %+v", result)
	}
}
