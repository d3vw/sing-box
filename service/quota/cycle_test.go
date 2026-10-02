package quota

import (
	"context"
	"strings"
	"testing"
	"time"
)

func bjt(year int, month time.Month, day, hour, minute int) time.Time {
	return time.Date(year, month, day, hour, minute, 0, 0, quotaLocation)
}

func TestCycleBoundsUseBeijingMidnight(t *testing.T) {
	// 2026-10-31 16:30 UTC is already Nov 1 00:30 in Beijing.
	now := time.Date(2026, 10, 31, 16, 30, 0, 0, time.UTC)
	if got := cycleStartAt(1, now); !got.Equal(bjt(2026, 11, 1, 0, 0)) {
		t.Fatalf("unexpected cycle start %s", got)
	}
	if got := nextResetAt(1, now); !got.Equal(bjt(2026, 12, 1, 0, 0)) {
		t.Fatalf("unexpected next reset %s", got)
	}
	if got := cycleStartAt(15, bjt(2026, 1, 3, 12, 0)); !got.Equal(bjt(2025, 12, 15, 0, 0)) {
		t.Fatalf("expected cycle to start in the previous year, got %s", got)
	}
}

func TestCycleClampsToShortMonths(t *testing.T) {
	if got := nextResetAt(31, bjt(2026, 2, 10, 0, 0)); !got.Equal(bjt(2026, 2, 28, 0, 0)) {
		t.Fatalf("expected reset on Feb 28, got %s", got)
	}
	if got := nextResetAt(31, bjt(2026, 2, 28, 1, 0)); !got.Equal(bjt(2026, 3, 31, 0, 0)) {
		t.Fatalf("expected reset on Mar 31, got %s", got)
	}
}

func TestRollCycleResetsUsageOnlyAfterBoundary(t *testing.T) {
	m := newTestPortalManager()
	m.SetResetDay(1)
	state := m.userState(testInboundTag, testUserName)
	if m.RollCycle(bjt(2026, 10, 20, 12, 0)) {
		t.Fatal("first roll should only record the cycle")
	}
	if m.RollCycle(bjt(2026, 10, 31, 23, 59)) || state.Uplink.Load() != 40 {
		t.Fatal("usage must be kept within the cycle")
	}
	if !m.RollCycle(bjt(2026, 11, 1, 0, 1)) || state.Uplink.Load() != 0 || state.Downlink.Load() != 0 {
		t.Fatalf("expected usage reset after the boundary, got %d/%d", state.Uplink.Load(), state.Downlink.Load())
	}
	if m.RollCycle(bjt(2026, 11, 2, 0, 0)) {
		t.Fatal("expected one reset per cycle")
	}
}

func TestRollCycleWithoutResetDayKeepsUsage(t *testing.T) {
	m := newTestPortalManager()
	if m.RollCycle(time.Now()) {
		t.Fatal("expected no reset without reset_day")
	}
	if _, ok := m.NextReset(time.Now()); ok {
		t.Fatal("expected no next reset without reset_day")
	}
}

func TestCacheCarriesCycleAcrossRestart(t *testing.T) {
	before := &Service{ctx: context.Background(), manager: newTestPortalManager()}
	before.manager.SetResetDay(1)
	before.manager.RollCycle(bjt(2026, 10, 20, 12, 0))
	cache, err := before.encodeCache()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cache), `"cycle_start"`) {
		t.Fatalf("expected cycle_start in cache, got %s", cache)
	}

	// Restarting within the same cycle keeps usage; after reset day resets it.
	for _, tc := range []struct {
		now  time.Time
		want int64
	}{{bjt(2026, 10, 25, 0, 0), 40}, {bjt(2026, 11, 3, 0, 0), 0}} {
		after := &Service{ctx: context.Background(), manager: newTestPortalManager()}
		after.manager.SetResetDay(1)
		if err = after.decodeCache(cache); err != nil {
			t.Fatal(err)
		}
		after.manager.RollCycle(tc.now)
		if got := after.manager.userState(testInboundTag, testUserName).Uplink.Load(); got != tc.want {
			t.Fatalf("restart at %s: expected uplink %d, got %d", tc.now, tc.want, got)
		}
	}
}
