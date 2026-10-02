package quota

import "time"

// quotaLocation is Beijing time, which both the monthly reset and the
// portal's charts use regardless of the server's own timezone.
var quotaLocation = time.FixedZone("BJT", 8*60*60)

// cycleDay is the reset_day-th day of the given month at 00:00 BJT, clamped
// to the month's last day so reset_day 31 resets on Feb 28/29.
func cycleDay(year int, month time.Month, day int) time.Time {
	first := time.Date(year, month, 1, 0, 0, 0, 0, quotaLocation)
	if last := first.AddDate(0, 1, -1).Day(); day > last {
		day = last
	}
	return first.AddDate(0, 0, day-1)
}

// cycleStartAt returns the start of the reset cycle containing now.
func cycleStartAt(resetDay int, now time.Time) time.Time {
	local := now.In(quotaLocation)
	start := cycleDay(local.Year(), local.Month(), resetDay)
	if local.Before(start) {
		start = cycleDay(local.Year(), local.Month()-1, resetDay)
	}
	return start
}

// nextResetAt returns when the reset cycle containing now ends.
func nextResetAt(resetDay int, now time.Time) time.Time {
	start := cycleStartAt(resetDay, now)
	return cycleDay(start.Year(), start.Month()+1, resetDay)
}

// SetResetDay enables a monthly reset on the given day (1–31); 0 disables it.
func (m *Manager) SetResetDay(day int) {
	m.resetDay = day
}

// NextReset returns when usage next resets, or ok=false without reset_day.
func (m *Manager) NextReset(now time.Time) (resetAt time.Time, ok bool) {
	if m.resetDay == 0 {
		return time.Time{}, false
	}
	return nextResetAt(m.resetDay, now), true
}

// RollCycle resets every user's usage when now has entered a reset cycle
// after the one usage was last counted in, and reports whether it did. The
// first call only records the current cycle, so enabling reset_day or
// upgrading from a cache without cycle_start keeps existing usage.
func (m *Manager) RollCycle(now time.Time) bool {
	if m.resetDay == 0 {
		return false
	}
	start := cycleStartAt(m.resetDay, now)
	m.cycleAccess.Lock()
	previous := m.cycleStart
	if !start.After(previous) {
		m.cycleAccess.Unlock()
		return false
	}
	m.cycleStart = start
	m.cycleAccess.Unlock()
	if previous.IsZero() {
		return false
	}
	m.access.RLock()
	defer m.access.RUnlock()
	for _, state := range m.users {
		state.Uplink.Store(0)
		state.Downlink.Store(0)
	}
	return true
}

func (m *Manager) currentCycleStart() time.Time {
	m.cycleAccess.Lock()
	defer m.cycleAccess.Unlock()
	return m.cycleStart
}

func (m *Manager) restoreCycleStart(start time.Time) {
	m.cycleAccess.Lock()
	defer m.cycleAccess.Unlock()
	m.cycleStart = start
}
