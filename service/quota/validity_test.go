package quota

import (
	"strings"
	"testing"
	"time"

	"github.com/sagernet/sing-box/option"
)

func TestExpiresAtAddsValidityToStartDate(t *testing.T) {
	for _, c := range []struct {
		start, validity string
		want            time.Time
	}{
		{"2026-10-01", "month", bjt(2026, 11, 1, 0, 0)},
		{"2026-11-15", "quarter", bjt(2027, 2, 15, 0, 0)},
		{"2026-10-01", "half_year", bjt(2027, 4, 1, 0, 0)},
		{"2026-10-01", "year", bjt(2027, 10, 1, 0, 0)},
		// A month from Jan 31 ends on the last day of February.
		{"2026-01-31", "month", bjt(2026, 2, 28, 0, 0)},
	} {
		got, ok, err := expiresAt(option.QuotaValidity{StartDate: c.start, Validity: c.validity})
		if err != nil || !ok || !got.Equal(c.want) {
			t.Fatalf("%s + %s: got %s, %v, %v; want %s", c.start, c.validity, got, ok, err, c.want)
		}
	}
}

func TestExpiresAtRejectsInvalidPeriods(t *testing.T) {
	if _, ok, err := expiresAt(option.QuotaValidity{}); ok || err != nil {
		t.Fatal("expected no period without start_date and validity")
	}
	for _, v := range []option.QuotaValidity{
		{StartDate: "2026-10-01"},
		{Validity: "month"},
		{StartDate: "2026/10/01", Validity: "month"},
		{StartDate: "2026-10-01", Validity: "week"},
	} {
		if _, _, err := expiresAt(v); err == nil {
			t.Fatalf("expected %+v to be rejected", v)
		}
	}
}

func TestValidUntilCountsToday(t *testing.T) {
	expiry := bjt(2026, 11, 1, 0, 0)
	for _, c := range []struct {
		now  time.Time
		left int
	}{
		{bjt(2026, 10, 23, 12, 0), 9},
		{bjt(2026, 10, 31, 23, 0), 1},
		{expiry, 0},
	} {
		lastDay, left := validUntil(expiry, c.now)
		if !lastDay.Equal(bjt(2026, 10, 31, 0, 0)) || left != c.left {
			t.Fatalf("at %s: got %s, %d left; want Oct 31, %d left", c.now, lastDay, left, c.left)
		}
	}
}

func TestPortalModernShowsExpiry(t *testing.T) {
	h, now := newTestDashboard(t)
	h.manager.userState(testInboundTag, testUserName).ExpiresAt = bjt(2026, 10, 27, 0, 0)
	page := h.renderModernAt(testInboundTag, testUserName, true, now)
	for _, want := range []string{
		`"expiry":{"y":2026,"m":10,"d":26,"left":5}`,
		`class="reset-line expiry-line warn"`,
		`data-i18n="validTo" data-a="2026-10-26"`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("expected %q, got:\n%s", want, page)
		}
	}
	page = h.renderModernAt(testInboundTag, testUserName, true, bjt(2026, 10, 28, 9, 0))
	for _, want := range []string{`"left":0`, `class="reset-line expiry-line err"`, `data-i18n="expired"`, `data-i18n="expiredOn"`} {
		if !strings.Contains(page, want) {
			t.Fatalf("expected %q once expired, got:\n%s", want, page)
		}
	}
}

func TestPortalModernForecastRunsToEarlierOfResetAndExpiry(t *testing.T) {
	h, now := newTestDashboard(t)
	state := h.manager.userState(testInboundTag, testUserName)
	// Without reset_day the forecast runs to expiry.
	h.manager.SetResetDay(0)
	state.ExpiresAt = bjt(2026, 12, 1, 0, 0)
	page := h.renderModernAt(testInboundTag, testUserName, false, now)
	for _, want := range []string{`"end":"expiry"`, `class="fc-tag fc-m`, `class="bl budget"`, `"reset":null`} {
		if !strings.Contains(page, want) {
			t.Fatalf("expected %q without reset_day, got:\n%s", want, page)
		}
	}
	// The Nov 1 reset comes before the Dec 1 expiry.
	h.manager.SetResetDay(1)
	if page = h.renderModernAt(testInboundTag, testUserName, false, now); !strings.Contains(page, `"end":"reset"`) {
		t.Fatal("expected the forecast to run to the earlier reset")
	}
	// An expiry before the reset ends the forecast there.
	state.ExpiresAt = bjt(2026, 10, 27, 0, 0)
	if page = h.renderModernAt(testInboundTag, testUserName, false, now); !strings.Contains(page, `"end":"expiry"`) {
		t.Fatal("expected the forecast to run to the earlier expiry")
	}
	// Once expired, only the reset is left to forecast to.
	h.manager.SetResetDay(0)
	if page = h.renderModernAt(testInboundTag, testUserName, false, bjt(2026, 10, 28, 9, 0)); strings.Contains(page, `class="fc-tag`) {
		t.Fatal("expected no forecast once expired without reset_day")
	}
}
